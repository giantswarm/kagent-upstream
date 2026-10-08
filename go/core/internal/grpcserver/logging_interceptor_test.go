package grpcserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/pkg/logging"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// Proves the otelgrpc stats handler has started the server span by the time
// the logging interceptor builds the request logger.
func TestServerRequestLogCarriesServerSpan(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	previousTracerProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(tracerProvider)
	var output lockedBuffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
		otel.SetTracerProvider(previousTracerProvider)
		require.NoError(t, tracerProvider.Shutdown(context.Background()))
	})

	listener := bufconn.Listen(1024 * 1024)
	server, err := New(Config{
		Listener:      listener,
		SystemService: testSystemService(),
		Registerer:    prometheus.NewRegistry(),
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })

	_, err = apiv1alpha1.NewSystemServiceClient(connection).GetVersion(t.Context(), &apiv1alpha1.GetVersionRequest{})
	require.NoError(t, err)

	// The client has its response before the server is done with the RPC:
	// otelgrpc ends the server span on the stats handler's End event, after
	// the response is written. Wait for the span and the log record instead
	// of assuming either is there when GetVersion returns.
	var completed map[string]any
	require.Eventually(t, func() bool {
		if len(spanRecorder.Ended()) == 0 {
			return false
		}
		for _, record := range decodeLogRecords(t, bytes.NewBuffer(output.Bytes())) {
			if record["msg"] == "rpc completed" {
				completed = record
			}
		}
		return completed != nil
	}, 10*time.Second, 10*time.Millisecond)

	spans := spanRecorder.Ended()
	require.Len(t, spans, 1)
	serverSpan := spans[0].SpanContext()
	require.Equal(t, serverSpan.TraceID().String(), completed["trace_id"])
	require.Equal(t, serverSpan.SpanID().String(), completed["span_id"])
}

func TestLoggingInterceptorsAddTraceContext(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(ctx context.Context, handler func(context.Context)) error
	}{
		{
			name: "unary",
			invoke: func(ctx context.Context, handler func(context.Context)) error {
				_, err := loggingUnaryInterceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: readMethod}, func(ctx context.Context, _ any) (any, error) {
					handler(ctx)
					return nil, nil
				})
				return err
			},
		},
		{
			name: "stream",
			invoke: func(ctx context.Context, handler func(context.Context)) error {
				stream := &contextServerStream{ctx: ctx}
				return loggingStreamInterceptor(nil, stream, &grpc.StreamServerInfo{FullMethod: readMethod}, func(_ any, stream grpc.ServerStream) error {
					handler(stream.Context())
					return nil
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(tracetest.NewSpanRecorder()))
			t.Cleanup(func() { require.NoError(t, tracerProvider.Shutdown(context.Background())) })

			var output bytes.Buffer
			ctx := logging.IntoContext(t.Context(), slog.New(slog.NewJSONHandler(&output, nil)))
			ctx, span := tracerProvider.Tracer("test").Start(ctx, readMethod)
			defer span.End()

			err := tt.invoke(ctx, func(ctx context.Context) {
				logging.FromContext(ctx).InfoContext(ctx, "handler ran")
			})
			require.NoError(t, err)

			records := decodeLogRecords(t, &output)
			require.Len(t, records, 2)
			require.Equal(t, "handler ran", records[0]["msg"])
			require.Equal(t, "rpc completed", records[1]["msg"])
			for _, record := range records {
				require.Equal(t, span.SpanContext().TraceID().String(), record["trace_id"])
				require.Equal(t, span.SpanContext().SpanID().String(), record["span_id"])
			}
		})
	}
}

func TestLoggingInterceptorOmitsTraceContextWithoutSpan(t *testing.T) {
	var output bytes.Buffer
	ctx := logging.IntoContext(t.Context(), slog.New(slog.NewJSONHandler(&output, nil)))

	_, err := loggingUnaryInterceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: readMethod}, func(context.Context, any) (any, error) {
		return nil, nil
	})
	require.NoError(t, err)

	records := decodeLogRecords(t, &output)
	require.Len(t, records, 1)
	require.NotContains(t, records[0], "trace_id")
	require.NotContains(t, records[0], "span_id")
}

// lockedBuffer is a log sink the server's goroutines write while the test reads it.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

// Bytes returns a copy of everything written so far.
func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buffer.Bytes())
}

func decodeLogRecords(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	decoder := json.NewDecoder(output)
	for decoder.More() {
		var record map[string]any
		require.NoError(t, decoder.Decode(&record))
		records = append(records, record)
	}
	return records
}
