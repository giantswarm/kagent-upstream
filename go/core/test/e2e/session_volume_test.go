package e2e_test

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
	kagentenv "github.com/kagent-dev/kagent/go/core/pkg/env"
	"github.com/kagent-dev/kagent/go/core/test/grant"
)

// The lane configures the controller's jwt Session admission with these
// (.github/workflows/ci.yaml, config.sessions.admission.jwt): the JWKS behind a
// fixed Service the test points at its own grant signer, and the issuer and
// audience the signer's grants name.
const (
	grantJWKSService = "e2e-grant-jwks"
	grantIssuer      = "https://grant-issuer.e2e.kagent.dev"
	grantAudience    = "kagent-e2e"
	// grantSubject is the caller the test's requests name (x-user-id).
	grantSubject = "e2e"
)

// TestCreateSessionVolumeSourceNeedsAGrant proves on the installed controller,
// whose lane configures the jwt Session admission, that a create naming a
// volume source is refused before anything is reserved unless it carries a
// current grant of the admission's issuer for the caller and that volume,
// while a valid grant, and the same request without the fields, go on to the
// agent lookup as they always did.
func TestCreateSessionVolumeSourceNeedsAGrant(t *testing.T) {
	target := interactionTarget(t)
	signer := serveGrantSigner(t)
	conn := newControllerConn(t, target)
	ctx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(t.Context(), "x-user-id", grantSubject), 2*time.Minute)
	t.Cleanup(cancel)
	sessions := apiv1.NewSessionServiceClient(conn)
	// The agent does not exist: the refusal has to come from the admission,
	// which runs before the store looks the agent up.
	agent := &apiv1.ResourceReference{Namespace: "kagent", Name: "no-such-agent-" + uuid.NewString()[:8]}
	volume := &apiv1.SessionVolume{CsiDriver: "efs.csi.aws.com", VolumeHandle: "fs-0123456789abcdef0::fsap-0123456789abcdef0"}
	otherVolume := &apiv1.SessionVolume{CsiDriver: volume.GetCsiDriver(), VolumeHandle: "fs-0123456789abcdef0::fsap-fedcba9876543210f"}
	source := &apiv1.SessionVolumeSource{
		Volume: volume,
		Mounts: []*apiv1.SessionVolumeMount{
			{SubPath: "sessions/${SESSION_ID}", MountPath: "/workspace"},
			{SubPath: "mirrors", MountPath: "/mirrors", ReadOnly: true},
		},
	}
	// signed is a grant of the lane's issuer for subject, admitting the source's
	// layout on volume, valid for ttl; a negative ttl has it expired.
	signed := func(subject string, volume *apiv1.SessionVolume, ttl time.Duration) string {
		token, err := signer.SignGrant(subject, grant.Grant{
			AgentNamespace: agent.GetNamespace(),
			Volume:         grant.Volume{Driver: volume.GetCsiDriver(), Handle: volume.GetVolumeHandle()},
			Mounts:         []grant.Mount{{SubPath: "sessions/${SESSION_ID}"}, {SubPath: "mirrors", ReadOnly: true}},
		}, ttl)
		require.NoError(t, err)
		return token
	}
	create := func(token string) *apiv1.CreateSessionRequest {
		return &apiv1.CreateSessionRequest{Agent: agent, RequestId: uuid.NewString(), VolumeSource: source, AdmissionToken: token}
	}
	// The admission fetches the JWKS on the first grant; until the Service's
	// endpoints have reached the controller that fetch answers Unavailable.
	valid := create(signed(grantSubject, volume, 10*time.Minute))
	require.NoError(t, wait.PollUntilContextTimeout(ctx, time.Second, time.Minute, true, func(ctx context.Context) (bool, error) {
		_, err := sessions.CreateSession(ctx, valid)
		return status.Code(err) != codes.Unavailable, nil
	}), "the admission never fetched the grant signer's JWKS")
	for _, tc := range []struct {
		name    string
		request *apiv1.CreateSessionRequest
		code    codes.Code
		message string
	}{
		{name: "volume source without a grant", request: &apiv1.CreateSessionRequest{Agent: agent, RequestId: uuid.NewString(), VolumeSource: source}, code: codes.PermissionDenied, message: "no admission grant"},
		{name: "volume source with a grant that is no JWT", request: create("grant"), code: codes.PermissionDenied},
		{name: "an expired grant", request: create(signed(grantSubject, volume, -time.Hour)), code: codes.PermissionDenied, message: "admission grant expired"},
		{name: "another person's grant", request: create(signed("someone-else", volume, 10*time.Minute)), code: codes.PermissionDenied, message: "admission grant names another person"},
		{name: "a grant for another volume", request: create(signed(grantSubject, otherVolume, 10*time.Minute)), code: codes.PermissionDenied, message: "admission grant names another volume"},
		{name: "a valid grant", request: valid, code: codes.FailedPrecondition, message: "Agent does not have a ready prepared revision"},
		{name: "a grant without a volume source", request: &apiv1.CreateSessionRequest{Agent: agent, RequestId: uuid.NewString(), AdmissionToken: "grant"}, code: codes.InvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sessions.CreateSession(ctx, tc.request)
			require.Equal(t, tc.code, status.Code(err), "error = %v", err)
			if tc.message != "" {
				require.ErrorContains(t, err, tc.message)
			}
		})
	}
	t.Run("without the fields", func(t *testing.T) {
		_, err := sessions.CreateSession(ctx, &apiv1.CreateSessionRequest{Agent: agent, RequestId: uuid.NewString()})
		require.Equal(t, codes.FailedPrecondition, status.Code(err), "error = %v", err)
		require.ErrorContains(t, err, "Agent does not have a ready prepared revision")
	})
	t.Run("malformed source", func(t *testing.T) {
		malformed := &apiv1.SessionVolumeSource{
			Volume: volume,
			Mounts: []*apiv1.SessionVolumeMount{{SubPath: "sessions/${SESSION_ID}", MountPath: "/data/workspace"}},
		}
		_, err := sessions.CreateSession(ctx, &apiv1.CreateSessionRequest{Agent: agent, RequestId: uuid.NewString(), VolumeSource: malformed, AdmissionToken: "grant"})
		require.Equal(t, codes.InvalidArgument, status.Code(err), "error = %v", err)
	})
}

// serveGrantSigner starts a grant signer of the lane's issuer and serves its
// JWKS from the test host behind the fixed Service the controller's admission
// fetches it from, for as long as the test runs.
func serveGrantSigner(t *testing.T) *grant.Signer {
	t.Helper()
	host := kagentenv.KagentLocalHost.Get()
	if host == "" {
		host = "172.17.0.1"
	}
	address := net.ParseIP(host)
	if address == nil {
		t.Skipf("the grant signer's Service needs the test host's IP address, KAGENT_E2E_LOCAL_HOST is %q", host)
	}
	signer, err := grant.NewSigner(grantIssuer, grantAudience)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	server := &http.Server{Handler: signer.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	_, hostPort, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	number, err := strconv.ParseInt(hostPort, 10, 32)
	require.NoError(t, err)

	kube := interactionKubeClient(t)
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: grantJWKSService, Namespace: "kagent"}}
	// An earlier run can have left the Service pointing at its own port.
	if err := kube.Delete(t.Context(), service); err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
	service.Spec = corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(int32(number))}}}
	require.NoError(t, wait.PollUntilContextTimeout(t.Context(), time.Second, time.Minute, true, func(ctx context.Context) (bool, error) {
		err := kube.Create(ctx, service)
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return err == nil, err
	}), "create the grant signer's Service")
	t.Cleanup(func() {
		if err := kube.Delete(context.Background(), service); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete the grant signer's Service: %v", err)
		}
	})
	addressType := discoveryv1.AddressTypeIPv4
	if address.To4() == nil {
		addressType = discoveryv1.AddressTypeIPv6
	}
	endpoints := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Name: service.Name, Namespace: service.Namespace,
			Labels:          map[string]string{discoveryv1.LabelServiceName: service.Name, discoveryv1.LabelManagedBy: "kagent-e2e"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Service", Name: service.Name, UID: service.UID}}},
		AddressType: addressType,
		Ports:       []discoveryv1.EndpointPort{{Name: new("http"), Port: new(int32(number)), Protocol: new(corev1.ProtocolTCP)}},
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{address.String()}, Conditions: discoveryv1.EndpointConditions{Ready: new(true)}}},
	}
	require.NoError(t, kube.Create(t.Context(), endpoints), "create the grant signer's EndpointSlice")
	return signer
}
