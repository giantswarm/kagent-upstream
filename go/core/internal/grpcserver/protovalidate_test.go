package grpcserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	protovalidatemiddleware "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/protovalidate"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestProtovalidateUnaryInterceptor(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}

	handled := false
	_, err = protovalidatemiddleware.UnaryServerInterceptor(validator)(
		t.Context(),
		&apiv1alpha1.CreateSessionRequest{},
		&grpc.UnaryServerInfo{},
		func(context.Context, any) (any, error) {
			handled = true
			return nil, nil
		},
	)
	if handled {
		t.Fatal("handler called for invalid request")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("validation code = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if details := status.Convert(err).Details(); len(details) != 1 {
		t.Fatalf("validation details = %d, want 1", len(details))
	}
}

func TestSessionRequestValidation(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		request proto.Message
		valid   bool
	}{
		{"ordinary name", &apiv1alpha1.CreateSessionRequest{Agent: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", Name: "Deploy 🚀"}, true},
		{"list without namespace", &apiv1alpha1.ListSessionsRequest{}, true},
		{"missing target namespace", &apiv1alpha1.ListSessionsRequest{Agent: &apiv1alpha1.ResourceReference{Name: "assistant"}}, false},
		{"leading whitespace", &apiv1alpha1.CreateSessionRequest{Agent: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", Name: " title"}, false},
		{"control character", &apiv1alpha1.CreateSessionRequest{Agent: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", Name: "first\nsecond"}, false},
		{"volume source", createSessionWithVolume(func(*apiv1alpha1.SessionVolumeSource) {}), true},
		{"volume source without a storage class", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.StorageClass = "" }), true},
		{"volume source with a fractional capacity", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.Capacity = "1.5Gi" }), true},
		{"volume source without a snapshot", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot = nil }), false},
		{"volume source with an uppercase driver", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot.CsiDriver = "EBS.csi.aws.com" }), false},
		{"volume source with an overlong driver", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot.CsiDriver = strings.Repeat("a", 64) }), false},
		{"volume source with an empty handle", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot.SnapshotHandle = "" }), false},
		{"volume source with whitespace in the handle", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot.SnapshotHandle = "snap 01" }), false},
		{"volume source with an overlong handle", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.Snapshot.SnapshotHandle = strings.Repeat("a", 1025) }), false},
		{"volume source without a capacity", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.Capacity = "" }), true},
		{"volume source with a non-quantity capacity", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.Capacity = "twenty" }), false},
		{"volume source with a negative capacity", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.Capacity = "-1Gi" }), false},
		{"volume source with an uppercase storage class", createSessionWithVolume(func(s *apiv1alpha1.SessionVolumeSource) { s.StorageClass = "GP3" }), false},
		{"admission token", &apiv1alpha1.CreateSessionRequest{Agent: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", AdmissionToken: "eyJhbGciOiJFUzI1NiJ9.e30.sig"}, true},
		{"admission token with whitespace", &apiv1alpha1.CreateSessionRequest{Agent: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", AdmissionToken: "two words"}, false},
		{"overlong admission token", &apiv1alpha1.CreateSessionRequest{Agent: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", AdmissionToken: strings.Repeat("a", 16385)}, false},
		{"invalid template filter", &apiv1alpha1.ListSessionsRequest{Agent: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "NOT A NAME"}}, false},
		{"valid rename", &apiv1alpha1.UpdateSessionNameRequest{SessionId: "11111111-1111-4111-8111-111111111111", Name: "New title"}, true},
		{"invalid rename id", &apiv1alpha1.UpdateSessionNameRequest{SessionId: "not-a-uuid", Name: "New title"}, false},
		{"valid checkpoint rename", &apiv1alpha1.UpdateCheckpointNameRequest{CheckpointId: "11111111-1111-4111-8111-111111111111", Name: "Before the detour"}, true},
		{"empty checkpoint rename", &apiv1alpha1.UpdateCheckpointNameRequest{CheckpointId: "11111111-1111-4111-8111-111111111111"}, true},
		{"checkpoint without selected task", &apiv1alpha1.CreateCheckpointRequest{SessionId: "11111111-1111-4111-8111-111111111111", RequestId: "request"}, false},
		{"checkpoint selected task", &apiv1alpha1.CreateCheckpointRequest{SessionId: "11111111-1111-4111-8111-111111111111", RequestId: "request", ExpectedHeadTaskId: "task"}, true},
		{"checkpoint rename control character", &apiv1alpha1.UpdateCheckpointNameRequest{CheckpointId: "11111111-1111-4111-8111-111111111111", Name: "first\nsecond"}, false},
		{"share without ttl", &apiv1alpha1.CreateSessionShareRequest{SessionId: "11111111-1111-4111-8111-111111111111", Permission: apiv1alpha1.SessionSharePermission_SESSION_SHARE_PERMISSION_READ_WRITE}, true},
		{"share positive ttl", &apiv1alpha1.CreateSessionShareRequest{SessionId: "11111111-1111-4111-8111-111111111111", Permission: apiv1alpha1.SessionSharePermission_SESSION_SHARE_PERMISSION_READ_WRITE, Ttl: durationpb.New(time.Hour)}, true},
		{"share zero ttl", &apiv1alpha1.CreateSessionShareRequest{SessionId: "11111111-1111-4111-8111-111111111111", Permission: apiv1alpha1.SessionSharePermission_SESSION_SHARE_PERMISSION_READ_WRITE, Ttl: durationpb.New(0)}, false},
		{"share negative ttl", &apiv1alpha1.CreateSessionShareRequest{SessionId: "11111111-1111-4111-8111-111111111111", Permission: apiv1alpha1.SessionSharePermission_SESSION_SHARE_PERMISSION_READ_WRITE, Ttl: durationpb.New(-time.Second)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validator.Validate(test.request)
			if (err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %t", err, test.valid)
			}
		})
	}
}

// createSessionWithVolume is a valid create request on a volume source, with
// mutate applied to the source.
func createSessionWithVolume(mutate func(*apiv1alpha1.SessionVolumeSource)) *apiv1alpha1.CreateSessionRequest {
	source := &apiv1alpha1.SessionVolumeSource{
		Snapshot:     &apiv1alpha1.SessionVolumeSnapshot{CsiDriver: "ebs.csi.aws.com", SnapshotHandle: "snap-0123456789abcdef0"},
		Capacity:     "20Gi",
		StorageClass: "gp3",
	}
	mutate(source)
	return &apiv1alpha1.CreateSessionRequest{
		Agent: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", VolumeSource: source,
	}
}

func TestInvalidSessionAndCheckpointIDsNeverReachHandlers(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}
	requests := []proto.Message{
		&apiv1alpha1.GetSessionRequest{SessionId: "invalid"},
		&apiv1alpha1.UpdateSessionNameRequest{SessionId: "invalid"},
		&apiv1alpha1.SuspendSessionRequest{SessionId: "invalid"},
		&apiv1alpha1.ResumeSessionRequest{SessionId: "invalid"},
		&apiv1alpha1.DeleteSessionRequest{SessionId: "invalid"},
		&apiv1alpha1.CreateSessionShareRequest{SessionId: "invalid", Permission: apiv1alpha1.SessionSharePermission_SESSION_SHARE_PERMISSION_READ_ONLY},
		&apiv1alpha1.ListSessionSharesRequest{SessionId: "invalid"},
		&apiv1alpha1.RevokeSessionShareRequest{ShareId: "invalid"},
		&apiv1alpha1.CreateCheckpointRequest{SessionId: "invalid", RequestId: "request"},
		&apiv1alpha1.GetCheckpointRequest{CheckpointId: "invalid"},
		&apiv1alpha1.ListCheckpointsRequest{SessionId: "invalid"},
		&apiv1alpha1.DeleteCheckpointRequest{CheckpointId: "invalid"},
		&apiv1alpha1.ForkSessionRequest{CheckpointId: "invalid", RequestId: "request"},
		&apiv1alpha1.UpdateCheckpointNameRequest{CheckpointId: "invalid"},
	}
	for _, request := range requests {
		t.Run(string(proto.MessageName(request)), func(t *testing.T) {
			_, err := protovalidatemiddleware.UnaryServerInterceptor(validator)(
				t.Context(), request, &grpc.UnaryServerInfo{},
				func(context.Context, any) (any, error) {
					t.Fatal("handler called with an invalid UUID")
					return nil, nil
				},
			)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("validation code = %v, want %v", status.Code(err), codes.InvalidArgument)
			}
		})
	}
}

func TestAgentRequestValidation(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}
	ref := &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "assistant"}
	resource := &apiv1alpha1.StructuredObject{}
	for _, test := range []struct {
		name    string
		request proto.Message
		valid   bool
	}{
		{"list", &apiv1alpha1.ListAgentsRequest{Namespace: "team-a"}, true},
		{"list missing namespace", &apiv1alpha1.ListAgentsRequest{}, false},
		{"list invalid namespace", &apiv1alpha1.ListAgentsRequest{Namespace: "team/a"}, false},
		{"get", &apiv1alpha1.GetAgentRequest{Ref: ref}, true},
		{"get missing ref", &apiv1alpha1.GetAgentRequest{}, false},
		{"get invalid ref", &apiv1alpha1.GetAgentRequest{Ref: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "INVALID"}}, false},
		{"create", &apiv1alpha1.CreateAgentRequest{Ref: ref, Resource: resource}, true},
		{"create missing ref", &apiv1alpha1.CreateAgentRequest{Resource: resource}, false},
		{"create missing resource", &apiv1alpha1.CreateAgentRequest{Ref: ref}, false},
		{"update", &apiv1alpha1.UpdateAgentRequest{Ref: ref, Resource: resource}, true},
		{"update missing ref", &apiv1alpha1.UpdateAgentRequest{Resource: resource}, false},
		{"update missing resource", &apiv1alpha1.UpdateAgentRequest{Ref: ref}, false},
		{"delete", &apiv1alpha1.DeleteAgentRequest{Ref: ref}, true},
		{"delete missing ref", &apiv1alpha1.DeleteAgentRequest{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			handled := false
			_, err := protovalidatemiddleware.UnaryServerInterceptor(validator)(
				t.Context(), test.request, &grpc.UnaryServerInfo{},
				func(context.Context, any) (any, error) {
					handled = true
					return nil, nil
				},
			)
			if handled != test.valid {
				t.Fatalf("handler called = %t, want %t", handled, test.valid)
			}
			want := codes.InvalidArgument
			if test.valid {
				want = codes.OK
			}
			if status.Code(err) != want {
				t.Fatalf("validation code = %v, want %v", status.Code(err), want)
			}
		})
	}
}
