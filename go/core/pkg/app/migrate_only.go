package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/config/v1"
	"github.com/kagent-dev/kagent/go/core/pkg/config"
	"github.com/kagent-dev/kagent/go/core/pkg/lineconvert"
	"github.com/kagent-dev/kagent/go/core/pkg/migrations"
)

// MigrateOnly migrates the database of input with the owner login and the
// sources Run would apply, then returns without starting the controller. It
// writes a lineconvert report of the database before and after to out, one
// JSON object per line, and fails when the conversion lost or altered a row.
// With dryRun it writes the report of the database as it is and changes
// nothing.
func MigrateOnly(ctx context.Context, input *configv1.ControllerConfig, opts Options, dryRun bool, out io.Writer) error {
	if input == nil {
		return errors.New("controller configuration is required")
	}
	cfg := proto.Clone(input).(*configv1.ControllerConfig)
	config.DefaultController(cfg)
	if err := config.Validate(cfg); err != nil {
		return err
	}
	if cfg.GetDatabase().GetSkipMigrations() {
		return errors.New("database.skipMigrations is set: the migration job needs the owner login")
	}
	ownerConfig, err := databasePoolConfig("database.owner", cfg.GetDatabase().GetOwner())
	if err != nil {
		return err
	}
	schema := cfg.GetDatabase().GetSchema()
	report := func(phase string) (lineconvert.Report, error) {
		pool, err := pgxpool.NewWithConfig(ctx, ownerConfig.Copy())
		if err != nil {
			return lineconvert.Report{}, fmt.Errorf("open database: %w", err)
		}
		defer pool.Close()
		r, err := lineconvert.Inspect(ctx, pool, schema)
		if err != nil {
			return r, fmt.Errorf("inspect the database %s the migrations: %w", phase, err)
		}
		line, err := json.Marshal(struct {
			Phase string `json:"phase"`
			lineconvert.Report
		}{phase, r})
		if err != nil {
			return r, err
		}
		_, err = fmt.Fprintf(out, "%s\n", line)
		return r, err
	}
	before, err := report("before")
	if err != nil {
		return err
	}
	if before.Counts["memory_ambiguous"] > 0 {
		return fmt.Errorf("%d memory rows name an Agent without its namespace and several namespaces hold one of that name: the conversion cannot key them", before.Counts["memory_ambiguous"])
	}
	if dryRun {
		return nil
	}
	if err := migrations.RunUp(ctx, ownerConfig, migrationSources(cfg.GetDatabase(), opts.ExtraMigrations)); err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}
	after, err := report("after")
	if err != nil {
		return err
	}
	if before.Shape == lineconvert.ShapeLine {
		if diffs := lineconvert.Compare(before, after); len(diffs) > 0 {
			return fmt.Errorf("the converted database differs from the one before: %s", strings.Join(diffs, "; "))
		}
	}
	return nil
}

// QuiesceDeployment scales the Deployment namespace/name to zero replicas and
// waits until none of its pods is left, so no controller of the previous
// release writes the database while it is migrated. A missing Deployment is
// already quiet.
func QuiesceDeployment(ctx context.Context, clientset kubernetes.Interface, namespace, name string, timeout time.Duration) error {
	deployments := clientset.AppsV1().Deployments(namespace)
	deployment, err := deployments.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read deployment %s/%s: %w", namespace, name, err)
	}
	scale, err := deployments.GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read the scale of %s/%s: %w", namespace, name, err)
	}
	if scale.Spec.Replicas != 0 {
		scale.Spec.Replicas = 0
		if _, err := deployments.UpdateScale(ctx, name, scale, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("scale %s/%s to zero: %w", namespace, name, err)
		}
	}
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return fmt.Errorf("read the selector of %s/%s: %w", namespace, name, err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
		if err != nil {
			return fmt.Errorf("list the pods of %s/%s: %w", namespace, name, err)
		}
		if len(runningPods(pods.Items, selector)) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("pods of %s/%s still running after %s", namespace, name, timeout)
		case <-time.After(2 * time.Second):
		}
	}
}

func runningPods(pods []corev1.Pod, selector labels.Selector) []corev1.Pod {
	var left []corev1.Pod
	for _, pod := range pods {
		if selector.Matches(labels.Set(pod.Labels)) && pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
			left = append(left, pod)
		}
	}
	return left
}
