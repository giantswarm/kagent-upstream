package grpcserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	protovalidatemiddleware "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/protovalidate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
)

func TestProtovalidateUnaryInterceptor(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}

	handled := false
	_, err = protovalidatemiddleware.UnaryServerInterceptor(validator)(
		t.Context(),
		&apiv1.CreateSessionRequest{},
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
		{"ordinary name", &apiv1.CreateSessionRequest{Agent: &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", Name: "Deploy 🚀"}, true},
		{"list without namespace", &apiv1.ListSessionsRequest{}, true},
		{"missing target namespace", &apiv1.ListSessionsRequest{Agent: &apiv1.ResourceReference{Name: "assistant"}}, false},
		{"leading whitespace", &apiv1.CreateSessionRequest{Agent: &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", Name: " title"}, false},
		{"control character", &apiv1.CreateSessionRequest{Agent: &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", Name: "first\nsecond"}, false},
		{"volume source", createSessionWithVolume(func(*apiv1.SessionVolumeSource) {}), true},
		{"volume source with one read-only mount", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Mounts = s.Mounts[1:] }), true},
		{"volume source without a volume", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Volume = nil }), false},
		{"volume source with an uppercase driver", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Volume.CsiDriver = "EFS.csi.aws.com" }), false},
		{"volume source with an overlong driver", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Volume.CsiDriver = strings.Repeat("a", 64) }), false},
		{"volume source with an empty handle", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Volume.VolumeHandle = "" }), false},
		{"volume source with whitespace in the handle", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Volume.VolumeHandle = "fs 01" }), false},
		{"volume source with an overlong handle", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Volume.VolumeHandle = strings.Repeat("a", 1025) }), false},
		{"volume source without mounts", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Mounts = nil }), false},
		{"volume source with an absolute sub-path", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Mounts[0].SubPath = "/sessions" }), false},
		{"volume source with a parent sub-path", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Mounts[0].SubPath = "sessions/../mirrors" }), false},
		{"volume source with an empty sub-path", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Mounts[0].SubPath = "" }), false},
		{"volume source with a relative mount path", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Mounts[0].MountPath = "workspace" }), false},
		{"volume source with a trailing slash mount path", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Mounts[0].MountPath = "/workspace/" }), false},
		{"volume source mounted at the durable directory", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Mounts[0].MountPath = "/data" }), false},
		{"volume source mounted under the durable directory", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Mounts[0].MountPath = "/data/workspace" }), false},
		{"volume source with two read-write mounts", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Mounts[1].ReadOnly = false }), false},
		{"volume source with two mounts at one path", createSessionWithVolume(func(s *apiv1.SessionVolumeSource) { s.Mounts[1].MountPath = "/workspace" }), false},
		{"admission token", &apiv1.CreateSessionRequest{Agent: &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", AdmissionToken: "eyJhbGciOiJFUzI1NiJ9.e30.sig"}, true},
		{"admission token with whitespace", &apiv1.CreateSessionRequest{Agent: &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", AdmissionToken: "two words"}, false},
		{"overlong admission token", &apiv1.CreateSessionRequest{Agent: &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", AdmissionToken: strings.Repeat("a", 16385)}, false},
		{"invalid template filter", &apiv1.ListSessionsRequest{Agent: &apiv1.ResourceReference{Namespace: "team-a", Name: "NOT A NAME"}}, false},
		{"valid rename", &apiv1.UpdateSessionNameRequest{SessionId: "11111111-1111-4111-8111-111111111111", Name: "New title"}, true},
		{"invalid rename id", &apiv1.UpdateSessionNameRequest{SessionId: "not-a-uuid", Name: "New title"}, false},
		{"valid checkpoint rename", &apiv1.UpdateCheckpointNameRequest{CheckpointId: "11111111-1111-4111-8111-111111111111", Name: "Before the detour"}, true},
		{"empty checkpoint rename", &apiv1.UpdateCheckpointNameRequest{CheckpointId: "11111111-1111-4111-8111-111111111111"}, true},
		{"checkpoint without selected task", &apiv1.CreateCheckpointRequest{SessionId: "11111111-1111-4111-8111-111111111111", RequestId: "request"}, false},
		{"checkpoint selected task", &apiv1.CreateCheckpointRequest{SessionId: "11111111-1111-4111-8111-111111111111", RequestId: "request", ExpectedHeadTaskId: "task"}, true},
		{"checkpoint rename control character", &apiv1.UpdateCheckpointNameRequest{CheckpointId: "11111111-1111-4111-8111-111111111111", Name: "first\nsecond"}, false},
		{"share without ttl", &apiv1.CreateSessionShareRequest{SessionId: "11111111-1111-4111-8111-111111111111"}, true},
		{"share positive ttl", &apiv1.CreateSessionShareRequest{SessionId: "11111111-1111-4111-8111-111111111111", Ttl: durationpb.New(time.Hour)}, true},
		{"share zero ttl", &apiv1.CreateSessionShareRequest{SessionId: "11111111-1111-4111-8111-111111111111", Ttl: durationpb.New(0)}, false},
		{"share negative ttl", &apiv1.CreateSessionShareRequest{SessionId: "11111111-1111-4111-8111-111111111111", Ttl: durationpb.New(-time.Second)}, false},
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
func createSessionWithVolume(mutate func(*apiv1.SessionVolumeSource)) *apiv1.CreateSessionRequest {
	source := &apiv1.SessionVolumeSource{
		Volume: &apiv1.SessionVolume{CsiDriver: "efs.csi.aws.com", VolumeHandle: "fs-0123456789abcdef0::fsap-0123456789abcdef0"},
		Mounts: []*apiv1.SessionVolumeMount{
			{SubPath: "sessions/${SESSION_ID}", MountPath: "/workspace"},
			{SubPath: "mirrors", MountPath: "/mirrors", ReadOnly: true},
		},
	}
	mutate(source)
	return &apiv1.CreateSessionRequest{
		Agent: &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}, RequestId: "request-1", VolumeSource: source,
	}
}

func TestInvalidSessionAndCheckpointIDsNeverReachHandlers(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatal(err)
	}
	requests := []proto.Message{
		&apiv1.GetSessionRequest{SessionId: "invalid"},
		&apiv1.UpdateSessionNameRequest{SessionId: "invalid"},
		&apiv1.SuspendSessionRequest{SessionId: "invalid"},
		&apiv1.ResumeSessionRequest{SessionId: "invalid"},
		&apiv1.DeleteSessionRequest{SessionId: "invalid"},
		&apiv1.CreateSessionShareRequest{SessionId: "invalid"},
		&apiv1.ListSessionSharesRequest{SessionId: "invalid"},
		&apiv1.RevokeSessionShareRequest{ShareId: "invalid"},
		&apiv1.CreateCheckpointRequest{SessionId: "invalid", RequestId: "request"},
		&apiv1.GetCheckpointRequest{CheckpointId: "invalid"},
		&apiv1.ListCheckpointsRequest{SessionId: "invalid"},
		&apiv1.DeleteCheckpointRequest{CheckpointId: "invalid"},
		&apiv1.ForkSessionRequest{CheckpointId: "invalid", RequestId: "request"},
		&apiv1.UpdateCheckpointNameRequest{CheckpointId: "invalid"},
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
	ref := &apiv1.ResourceReference{Namespace: "team-a", Name: "assistant"}
	resource := &apiv1.StructuredObject{}
	for _, test := range []struct {
		name    string
		request proto.Message
		valid   bool
	}{
		{"list", &apiv1.ListAgentsRequest{Namespace: "team-a"}, true},
		{"list every namespace", &apiv1.ListAgentsRequest{}, true},
		{"list page", &apiv1.ListAgentsRequest{Page: &apiv1.PageRequest{Limit: 100, PageToken: "token"}}, true},
		{"list page over the limit", &apiv1.ListAgentsRequest{Page: &apiv1.PageRequest{Limit: 101}}, false},
		{"template list every namespace", &apiv1.ListAgentTemplatesRequest{}, true},
		{"template list", &apiv1.ListAgentTemplatesRequest{Namespace: "team-a"}, true},
		{"template list invalid namespace", &apiv1.ListAgentTemplatesRequest{Namespace: "team/a"}, false},
		{"list invalid namespace", &apiv1.ListAgentsRequest{Namespace: "team/a"}, false},
		{"get", &apiv1.GetAgentRequest{Ref: ref}, true},
		{"get missing ref", &apiv1.GetAgentRequest{}, false},
		{"get invalid ref", &apiv1.GetAgentRequest{Ref: &apiv1.ResourceReference{Namespace: "team-a", Name: "INVALID"}}, false},
		{"create", &apiv1.CreateAgentRequest{Ref: ref, Resource: resource}, true},
		{"create missing ref", &apiv1.CreateAgentRequest{Resource: resource}, false},
		{"create missing resource", &apiv1.CreateAgentRequest{Ref: ref}, false},
		{"update", &apiv1.UpdateAgentRequest{Ref: ref, Resource: resource}, true},
		{"update missing ref", &apiv1.UpdateAgentRequest{Resource: resource}, false},
		{"update missing resource", &apiv1.UpdateAgentRequest{Ref: ref}, false},
		{"delete", &apiv1.DeleteAgentRequest{Ref: ref}, true},
		{"delete missing ref", &apiv1.DeleteAgentRequest{}, false},
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
