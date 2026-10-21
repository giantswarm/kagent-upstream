package taskstore

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/a2aproject/a2a-go/v2/a2a"
	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	"github.com/kagent-dev/kagent/go/core/pkg/auth"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type settlementStore struct {
	Store
	err       error
	commit    <-chan struct{}
	committed bool
}

var _ Store = (*settlementStore)(nil)

func (s *settlementStore) GetSessionForRuntime(_ context.Context, id, _ string) (*apiv1alpha1.Session, error) {
	return &apiv1alpha1.Session{Id: id, A2AAuthority: substrate.ActorHost("team-a", substrate.ActorName(id), "")}, nil
}

func (s *settlementStore) SettleSessionTaskPublished(context.Context, string, string, int64) (*a2a.Task, error) {
	<-s.commit
	s.committed = s.err == nil
	return nil, s.err
}

func TestSettleTaskWakesOnlyAfterCommit(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		queued bool
		want   codes.Code
	}{
		{name: "committed"},
		{name: "failed", err: database.ErrConflict, want: codes.Aborted},
		{name: "coalesced", queued: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				commit := make(chan struct{})
				defer close(commit)
				store := &settlementStore{err: test.err, commit: commit}
				wakes := make(chan struct{}, 1)
				if test.queued {
					wakes <- struct{}{}
				}
				before := len(wakes)
				service := NewService(store, wakes)
				ctx, cancel := context.WithCancel(auth.AuthSessionTo(t.Context(), runtimeSession{sessionID: "session", atespace: "team-a", actorUID: "actor"}))
				cancel() // A disconnected caller must not suppress the post-commit hint.
				done := make(chan error, 1)
				go func() {
					_, err := service.SettleTask(ctx, &apiv1alpha1.TaskStoreServiceSettleTaskRequest{SessionId: "session", TaskId: "task", Version: 1})
					done <- err
				}()
				synctest.Wait()
				require.False(t, store.committed)
				require.Len(t, wakes, before, "must not signal before the commit")
				commit <- struct{}{}
				require.Equal(t, test.want, status.Code(<-done), "a queued hint must not block settlement")
				if test.err == nil {
					require.True(t, store.committed)
					require.Len(t, wakes, 1)
				} else {
					require.False(t, store.committed)
					require.Empty(t, wakes)
				}
			})
		})
	}
}

// publishingStore publishes each boundary once, as the database does: a
// retried settlement of a published boundary returns no task.
type publishingStore struct {
	Store
	task      *a2a.Task
	published bool
}

func (s *publishingStore) GetSessionForRuntime(_ context.Context, id, _ string) (*apiv1alpha1.Session, error) {
	return &apiv1alpha1.Session{
		Id: id, A2AAuthority: substrate.ActorHost("team-a", substrate.ActorName(id), ""),
		Agent: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "sre"}, Creator: "jane@example.com",
	}, nil
}

func (s *publishingStore) SettleSessionTaskPublished(context.Context, string, string, int64) (*a2a.Task, error) {
	if s.published {
		return nil, nil
	}
	s.published = true
	return s.task, nil
}

func TestSettleTaskCountsTheForwarderRefusalsOfAPublishedBoundaryOnce(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	message := a2a.NewMessage(a2a.MessageRoleAgent)
	apia2a.SetForwarderRefusals(message, apia2a.ForwarderRefusals{Unauthorized: 2, RefusedPath: 1})
	task := &a2a.Task{ID: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: message}}
	service := NewService(&publishingStore{task: task}, nil)
	ctx := auth.AuthSessionTo(t.Context(), runtimeSession{sessionID: "session", atespace: "team-a", actorUID: "actor"})
	for range 2 {
		_, err := service.SettleTask(ctx, &apiv1alpha1.TaskStoreServiceSettleTaskRequest{SessionId: "session", TaskId: "task", Version: 1})
		require.NoError(t, err)
	}

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &collected))
	got := map[int64]int64{}
	for _, scope := range collected.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name != "kagent.harness.forwarder.refusals" {
				continue
			}
			for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
				namespace, _ := point.Attributes.Value("k8s.namespace.name")
				agent, _ := point.Attributes.Value("gen_ai.agent.name")
				require.Equal(t, "team-a", namespace.AsString())
				require.Equal(t, "sre", agent.AsString())
				code, _ := point.Attributes.Value("http.response.status_code")
				got[code.AsInt64()] = point.Value
			}
		}
	}
	require.Equal(t, map[int64]int64{401: 2, 400: 1}, got)
}

func TestSettleTaskCountsTheTurnCostOfAPublishedBoundaryOncePerCreator(t *testing.T) {
	registry := prometheus.NewRegistry()
	exporter, err := otelprometheus.New(otelprometheus.WithRegisterer(registry))
	require.NoError(t, err)
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	message := a2a.NewMessage(a2a.MessageRoleAgent)
	message.Metadata = map[string]any{apia2a.UsageMetadataKey: map[string]any{"costUsd": 1.25, "totalTokenCount": 900.0}}
	task := &a2a.Task{ID: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: message}}
	service := NewService(&publishingStore{task: task}, nil)
	ctx := auth.AuthSessionTo(t.Context(), runtimeSession{sessionID: "session", atespace: "team-a", actorUID: "actor"})
	for range 2 {
		_, err := service.SettleTask(ctx, &apiv1alpha1.TaskStoreServiceSettleTaskRequest{SessionId: "session", TaskId: "task", Version: 1})
		require.NoError(t, err)
	}

	families, err := registry.Gather()
	require.NoError(t, err)
	found := -1
	for i, family := range families {
		if family.GetName() == "kagent_session_turn_cost_usd_total" {
			found = i
		}
	}
	require.NotEqual(t, -1, found, "the scraped name of kagent.session.turn.cost.usd")
	cost := families[found]
	require.Len(t, cost.GetMetric(), 1)
	labels := map[string]string{}
	for _, label := range cost.GetMetric()[0].GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	require.Equal(t, "team-a", labels["k8s_namespace_name"])
	require.Equal(t, "sre", labels["gen_ai_agent_name"])
	require.Equal(t, "jane@example.com", labels["enduser_id"])
	require.InDelta(t, 1.25, cost.GetMetric()[0].GetCounter().GetValue(), 1e-9)
}

func TestSettleTaskCountsNoCostARuntimeDoesNotReport(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	message := a2a.NewMessage(a2a.MessageRoleAgent)
	message.Metadata = map[string]any{apia2a.UsageMetadataKey: map[string]any{"totalTokenCount": 900.0}}
	task := &a2a.Task{ID: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: message}}
	service := NewService(&publishingStore{task: task}, nil)
	ctx := auth.AuthSessionTo(t.Context(), runtimeSession{sessionID: "session", atespace: "team-a", actorUID: "actor"})
	_, err := service.SettleTask(ctx, &apiv1alpha1.TaskStoreServiceSettleTaskRequest{SessionId: "session", TaskId: "task", Version: 1})
	require.NoError(t, err)

	var collected metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &collected))
	for _, scope := range collected.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name == "kagent.session.turn.cost.usd" {
				require.Empty(t, measurement.Data.(metricdata.Sum[float64]).DataPoints)
			}
		}
	}
}
