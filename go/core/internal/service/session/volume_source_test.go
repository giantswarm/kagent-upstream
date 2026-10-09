package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/service/serviceerrors"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// recordingAdmission answers every request with err and records what it was
// asked, so a test can see what reaches the installation's admission.
type recordingAdmission struct {
	asked  bool
	caller string
	source *apiv1alpha1.SessionVolumeSource
	token  string
	err    error
}

func (a *recordingAdmission) Admit(_ context.Context, caller string, source *apiv1alpha1.SessionVolumeSource, token string) error {
	a.asked, a.caller, a.source, a.token = true, caller, source, token
	return a.err
}

func testVolumeSource() *apiv1alpha1.SessionVolumeSource {
	return &apiv1alpha1.SessionVolumeSource{
		Snapshot:     &apiv1alpha1.SessionVolumeSnapshot{CsiDriver: "ebs.csi.aws.com", SnapshotHandle: "snap-0123456789abcdef0"},
		Capacity:     "20Gi",
		StorageClass: "gp3",
	}
}

func testCreateRequest() CreateRequest {
	return CreateRequest{Agent: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestID: "request-1"}
}

func TestServiceCreateRefusesAVolumeSourceWithoutAnAdmission(t *testing.T) {
	for _, test := range []struct {
		name   string
		source *apiv1alpha1.SessionVolumeSource
		token  string
	}{
		{name: "volume source", source: testVolumeSource()},
		{name: "admission token", token: "grant"},
		{name: "both", source: testVolumeSource(), token: "grant"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &serviceTestStore{}
			service := NewService(store, serviceTestAuthorizer{}, serviceTestWorkflow{})
			request := testCreateRequest()
			request.VolumeSource, request.AdmissionToken = test.source, test.token
			_, err := service.Create(serviceTestContext("alice"), request)
			require.True(t, serviceerrors.IsCode(err, serviceerrors.CodeFailedPrecondition), "Create() error = %v", err)
			require.ErrorContains(t, err, "this installation admits no Session volume")
			require.Nil(t, store.createInput, "a refused request must not reserve a Session")
		})
	}
}

func TestServiceCreateAdmitsAVolumeSourceThroughTheAdmission(t *testing.T) {
	admission := &recordingAdmission{}
	store := &serviceTestStore{}
	service := NewService(store, serviceTestAuthorizer{}, serviceTestWorkflow{}, WithVolumeAdmission(admission))
	source := testVolumeSource()
	request := testCreateRequest()
	request.VolumeSource, request.AdmissionToken = source, "admission-token-never-stored"

	session, err := service.Create(serviceTestContext("alice"), request)
	require.NoError(t, err)
	require.Equal(t, "alice", admission.caller, "the admission sees the authenticated caller")
	require.True(t, proto.Equal(source, admission.source), "the admission sees the request's source")
	require.Equal(t, "admission-token-never-stored", admission.token)
	require.True(t, proto.Equal(source, store.createInput.GetVolumeSource()), "the reserved Session carries the source")
	require.True(t, proto.Equal(source, session.GetVolumeSource()), "the response carries the source")
	stored, err := proto.Marshal(store.createInput)
	require.NoError(t, err)
	require.NotContains(t, string(stored), "admission-token-never-stored", "the token reaches the store nowhere")
}

func TestServiceCreateMapsAnAdmissionRefusal(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code serviceerrors.Code
	}{
		{name: "the admission's own answer stands", err: serviceerrors.NewFailedPrecondition("grant expired", nil), code: serviceerrors.CodeFailedPrecondition},
		{name: "any other failure denies", err: errors.New("signature invalid"), code: serviceerrors.CodePermissionDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &serviceTestStore{}
			service := NewService(store, serviceTestAuthorizer{}, serviceTestWorkflow{}, WithVolumeAdmission(&recordingAdmission{err: test.err}))
			request := testCreateRequest()
			request.VolumeSource, request.AdmissionToken = testVolumeSource(), "grant"
			_, err := service.Create(serviceTestContext("alice"), request)
			require.True(t, serviceerrors.IsCode(err, test.code), "Create() error = %v, want code %s", err, test.code)
			require.Nil(t, store.createInput, "a refused request must not reserve a Session")
		})
	}
}

// A request without the volume fields never consults the admission and reserves
// a Session without a source, with or without an admission configured.
func TestServiceCreateWithoutTheVolumeFieldsNeverAsksTheAdmission(t *testing.T) {
	for _, test := range []struct {
		name    string
		options []Option
	}{
		{name: "no admission"},
		{name: "an admission that refuses everything", options: []Option{WithVolumeAdmission(&recordingAdmission{err: errors.New("must not be asked")})}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &serviceTestStore{}
			service := NewService(store, serviceTestAuthorizer{}, serviceTestWorkflow{}, test.options...)
			session, err := service.Create(serviceTestContext("alice"), testCreateRequest())
			require.NoError(t, err)
			require.Nil(t, store.createInput.GetVolumeSource())
			require.Nil(t, session.GetVolumeSource())
		})
	}
}

// Validation of the source precedes the admission: a malformed source is
// InvalidArgument whatever the installation configures, and never reaches it.
func TestServiceCreateRejectsAnInvalidVolumeSource(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*apiv1alpha1.SessionVolumeSource)
		want   string
	}{
		{name: "no snapshot", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot = nil }, want: "csi_driver"},
		{name: "driver with uppercase", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot.CsiDriver = "EBS.csi.aws.com" }, want: "csi_driver"},
		{name: "driver too long", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot.CsiDriver = strings.Repeat("a", 64) }, want: "csi_driver"},
		{name: "empty handle", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot.SnapshotHandle = "" }, want: "snapshot_handle"},
		{name: "handle with whitespace", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot.SnapshotHandle = "snap 01" }, want: "snapshot_handle"},
		{name: "handle with a control character", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot.SnapshotHandle = "snap\x0001" }, want: "snapshot_handle"},
		{name: "handle too long", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot.SnapshotHandle = strings.Repeat("a", 1025) }, want: "snapshot_handle"},
		{name: "capacity not a quantity", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.Capacity = "twenty" }, want: "capacity"},
		{name: "capacity zero", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.Capacity = "0" }, want: "capacity"},
		{name: "capacity negative", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.Capacity = "-1Gi" }, want: "capacity"},
		{name: "capacity in fractional bytes", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.Capacity = "500m" }, want: "capacity"},
		{name: "storage class with uppercase", mutate: func(s *apiv1alpha1.SessionVolumeSource) { s.StorageClass = "GP3" }, want: "storage_class"},
	} {
		t.Run(test.name, func(t *testing.T) {
			admission := &recordingAdmission{}
			store := &serviceTestStore{}
			service := NewService(store, serviceTestAuthorizer{}, serviceTestWorkflow{}, WithVolumeAdmission(admission))
			request := testCreateRequest()
			request.VolumeSource, request.AdmissionToken = testVolumeSource(), "grant"
			test.mutate(request.VolumeSource)
			_, err := service.Create(serviceTestContext("alice"), request)
			require.True(t, serviceerrors.IsCode(err, serviceerrors.CodeInvalidArgument), "Create() error = %v", err)
			require.ErrorContains(t, err, test.want)
			require.False(t, admission.asked, "an invalid source must not reach the admission")
			require.Nil(t, store.createInput)
		})
	}
	for _, test := range []struct {
		name     string
		capacity string
	}{
		{name: "fractional units of whole bytes are accepted", capacity: "1.5Gi"},
		// The service fixes no default size: an omitted capacity reaches the
		// admission as omitted, for it to size the volume for the snapshot.
		{name: "an omitted capacity is left to the admission", capacity: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			admission := &recordingAdmission{}
			store := &serviceTestStore{}
			service := NewService(store, serviceTestAuthorizer{}, serviceTestWorkflow{}, WithVolumeAdmission(admission))
			request := testCreateRequest()
			request.VolumeSource = testVolumeSource()
			request.VolumeSource.Capacity, request.VolumeSource.StorageClass = test.capacity, ""
			session, err := service.Create(serviceTestContext("alice"), request)
			require.NoError(t, err)
			require.Equal(t, test.capacity, admission.source.GetCapacity())
			require.Equal(t, test.capacity, session.GetVolumeSource().GetCapacity())
		})
	}
}
