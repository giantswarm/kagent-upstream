package e2e_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func newControllerConn(t *testing.T, target string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			return invoker(withA2AVersion(ctx, method), method, req, reply, cc, opts...)
		}),
		grpc.WithStreamInterceptor(func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
			return streamer(withA2AVersion(ctx, method), desc, cc, method, opts...)
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	require.NoError(t, waitForControllerAPI(t.Context(), conn), "controller API %s did not become reachable", target)
	return conn
}

// withA2AVersion sets A2A-Version on a call to the A2A service: a2a-go refuses
// a call without it as protocol 0.3. It replaces a value the caller set, so a
// fixture that also sets it does not send two versions, which a2a-go refuses too.
func withA2AVersion(ctx context.Context, method string) context.Context {
	if !strings.HasPrefix(method, "/"+a2apb.A2AService_ServiceDesc.ServiceName+"/") {
		return ctx
	}
	headers, _ := metadata.FromOutgoingContext(ctx)
	headers = headers.Copy()
	headers.Set(a2atype.SvcParamVersion, string(a2atype.Version))
	return metadata.NewOutgoingContext(ctx, headers)
}

func TestWithA2AVersion(t *testing.T) {
	a2aMethod := "/" + a2apb.A2AService_ServiceDesc.ServiceName + "/SendMessage"
	for _, test := range []struct {
		name   string
		method string
		ctx    context.Context
		want   []string
	}{
		{name: "A2A call without the header", method: a2aMethod, ctx: metadata.AppendToOutgoingContext(t.Context(), "x-user-id", "e2e"), want: []string{"1.0"}},
		{name: "A2A call that already sets it", method: a2aMethod, ctx: metadata.AppendToOutgoingContext(t.Context(), a2atype.SvcParamVersion, "1.0"), want: []string{"1.0"}},
		{name: "another service", method: "/grpc.health.v1.Health/Check", ctx: t.Context(), want: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers, _ := metadata.FromOutgoingContext(withA2AVersion(test.ctx, test.method))
			require.Equal(t, test.want, headers.Get(a2atype.SvcParamVersion))
		})
	}
}

func waitForControllerAPI(ctx context.Context, conn *grpc.ClientConn) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	// NewClient connects lazily. A fresh connection can time out while Service
	// routing settles after a controller rollout, even if a previous probe passed.
	// Wait only on this read-only probe; mutations keep their normal retry contract.
	response, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{}, grpc.WaitForReady(true))
	if err != nil {
		return err
	}
	if response.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("controller gRPC health is %s", response.GetStatus())
	}
	return nil
}

func TestControllerConnectionReadiness(t *testing.T) {
	for _, test := range []struct {
		name        string
		failedDials int32
		serverCode  codes.Code
		health      grpc_health_v1.HealthCheckResponse_ServingStatus
		wantCode    codes.Code
		wantCalls   int32
		wantError   string
	}{
		{name: "healthy", health: grpc_health_v1.HealthCheckResponse_SERVING, wantCalls: 1},
		{name: "first dial times out", failedDials: 1, health: grpc_health_v1.HealthCheckResponse_SERVING, wantCalls: 1},
		{name: "unreachable until deadline", failedDials: -1, wantCode: codes.DeadlineExceeded},
		{name: "server error is not retried", serverCode: codes.Unavailable, wantCode: codes.Unavailable, wantCalls: 1},
		{name: "not serving", health: grpc_health_v1.HealthCheckResponse_NOT_SERVING, wantCode: codes.Unknown, wantCalls: 1, wantError: "controller gRPC health is NOT_SERVING"},
		{name: "unknown health", health: grpc_health_v1.HealthCheckResponse_UNKNOWN, wantCode: codes.Unknown, wantCalls: 1, wantError: "controller gRPC health is UNKNOWN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener := bufconn.Listen(1024 * 1024)
			server := grpc.NewServer()
			service := &readinessHealthServer{code: test.serverCode, health: test.health}
			grpc_health_v1.RegisterHealthServer(server, service)
			serveErr := make(chan error, 1)
			go func() { serveErr <- server.Serve(listener) }()
			t.Cleanup(func() {
				server.Stop()
				require.NoError(t, <-serveErr)
			})

			var dials atomic.Int32
			retryBackoff := backoff.DefaultConfig
			retryBackoff.BaseDelay = 10 * time.Millisecond
			retryBackoff.MaxDelay = 10 * time.Millisecond
			conn, err := grpc.NewClient("passthrough:///controller",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithConnectParams(grpc.ConnectParams{Backoff: retryBackoff, MinConnectTimeout: time.Second}),
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
					attempt := dials.Add(1)
					if test.failedDials < 0 || attempt <= test.failedDials {
						return nil, &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
					}
					return listener.DialContext(ctx)
				}),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			timeout := 5 * time.Second
			if test.wantCode == codes.DeadlineExceeded {
				timeout = time.Second
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			err = waitForControllerAPI(ctx, conn)
			require.Equal(t, test.wantCode, status.Code(err), "%v", err)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
			}
			require.Equal(t, test.wantCalls, service.calls.Load())
			if test.failedDials >= 0 {
				require.Equal(t, test.failedDials+1, dials.Load())
			}
		})
	}
}

type readinessHealthServer struct {
	grpc_health_v1.UnimplementedHealthServer
	code   codes.Code
	health grpc_health_v1.HealthCheckResponse_ServingStatus
	calls  atomic.Int32
}

var _ grpc_health_v1.HealthServer = (*readinessHealthServer)(nil)

func (s *readinessHealthServer) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	s.calls.Add(1)
	return &grpc_health_v1.HealthCheckResponse{Status: s.health}, status.Error(s.code, "controller unavailable")
}
