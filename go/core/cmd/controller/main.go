/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	configv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/config/v1"
	authimpl "github.com/kagent-dev/kagent/go/core/internal/httpserver/auth"
	"github.com/kagent-dev/kagent/go/core/pkg/app"
	"github.com/kagent-dev/kagent/go/core/pkg/auth"
	"github.com/kagent-dev/kagent/go/core/pkg/config"
	"github.com/kagent-dev/kagent/go/core/pkg/crconvert"
	"github.com/kagent-dev/kagent/go/core/pkg/migrations"
)

func main() {
	path := flag.String("config", "/etc/kagent/controller.yaml", "Controller configuration file")
	check := flag.Bool("check-config", false, "Validate configuration and referenced files, then exit")
	level := flag.String("log-level", "", "Override the configured logging level")
	migrateOnly := flag.Bool("migrate-only", false, "Apply the database migrations with the owner login, report the database before and after, then exit")
	dryRun := flag.Bool("dry-run", false, "With -migrate-only, report the database and change nothing")
	quiesce := flag.String("quiesce-deployment", "", "With -migrate-only or -convert-crs backup, scale this namespace/name Deployment to zero and wait for its pods first")
	convertCRs := flag.String("convert-crs", "", "backup: save every api.kagent.dev object before the CRD upgrade; apply: write their v1.0.1 shape after it")
	backup := flag.String("cr-backup", "", "With -convert-crs, the namespace/name of the backup Secrets")
	flag.Parse()
	var err error
	if *convertCRs != "" {
		err = convertResources(*convertCRs, *backup, *quiesce)
	} else if *migrateOnly {
		err = migrate(*path, *dryRun, *quiesce)
	} else {
		err = run(*path, *check, *level)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(path string, check bool, level string) error {
	cfg := &configv1.ControllerConfig{}
	if err := config.Load(path, cfg); err != nil {
		return err
	}
	config.DefaultController(cfg)
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "log-level" {
			cfg.Logging.Level = &level
		}
	})
	if err := config.Validate(cfg); err != nil {
		return err
	}
	config.ResolveControllerPaths(cfg, path)
	paths := []string{cfg.GetDatabase().GetReadWrite().GetUrlFile(), cfg.GetPush().GetSigningPrivateKeyFile()}
	// The owner login is unused when migrations are skipped, and its Secret may not be mounted.
	if !cfg.GetDatabase().GetSkipMigrations() {
		paths = append(paths, cfg.GetDatabase().GetOwner().GetUrlFile())
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, err := config.ReadSecret(path); err != nil {
			return err
		}
	}
	if check {
		return nil
	}
	if err := app.SetupLogger(cfg.GetLogging().GetLevel()); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	authenticator, err := controllerAuthenticator(cfg.GetServer().GetAuth().GetMode(), cfg.GetServer().GetAuth().GetUserIdClaim())
	if err != nil {
		return err
	}
	// These downward-API inputs describe this pod, not runtime defaults.
	podTelemetryDefaults(cfg, os.Getenv) //nolint:forbidigo // Private pod identity supplied by Helm.
	if err := app.Run(ctx, cfg, app.Options{Authenticator: authenticator, ExtraMigrations: migrations.LineSources(cfg.GetDatabase().GetSchema())}); err != nil {
		slog.Default().ErrorContext(ctx, "controller stopped", "error", err)
		return err
	}
	return nil
}

// migrate runs the line's migration job: the previous release's controller
// stopped, then every migration source applied by the owner login, the line's
// conversion of a 1.x database among them.
func migrate(path string, dryRun bool, quiesce string) error {
	cfg := &configv1.ControllerConfig{}
	if err := config.Load(path, cfg); err != nil {
		return err
	}
	config.DefaultController(cfg)
	if err := config.Validate(cfg); err != nil {
		return err
	}
	config.ResolveControllerPaths(cfg, path)
	if err := app.SetupLogger(cfg.GetLogging().GetLevel()); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if quiesce != "" && !dryRun {
		restConfig, err := ctrlconfig.GetConfig()
		if err != nil {
			return fmt.Errorf("load Kubernetes config: %w", err)
		}
		if err := quiesceDeployment(ctx, restConfig, quiesce); err != nil {
			return err
		}
	}
	return app.MigrateOnly(ctx, cfg, app.Options{ExtraMigrations: migrations.LineSources(cfg.GetDatabase().GetSchema())}, dryRun, os.Stdout)
}

// convertResources runs one step of the line's CR conversion around the CRD
// upgrade. The backup step first stops the previous release's controller, so
// nothing reconciles the objects between the CRD upgrade and the apply step.
func convertResources(step, backup, quiesce string) error {
	namespace, name, ok := strings.Cut(backup, "/")
	if !ok || namespace == "" || name == "" {
		return fmt.Errorf("-cr-backup %q: want namespace/name", backup)
	}
	restConfig, err := ctrlconfig.GetConfig()
	if err != nil {
		return fmt.Errorf("load Kubernetes config: %w", err)
	}
	scheme := k8sruntime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	c, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var report crconvert.Report
	switch step {
	case "backup":
		if quiesce != "" {
			if err := quiesceDeployment(ctx, restConfig, quiesce); err != nil {
				return err
			}
		}
		report, err = crconvert.Backup(ctx, c, namespace, name)
	case "apply":
		report, err = crconvert.Apply(ctx, c, namespace, name)
	default:
		return fmt.Errorf("-convert-crs %q: want backup or apply", step)
	}
	if encoded, encodeErr := json.Marshal(report); encodeErr == nil {
		fmt.Println(string(encoded))
	}
	return err
}

func quiesceDeployment(ctx context.Context, restConfig *rest.Config, deployment string) error {
	namespace, name, ok := strings.Cut(deployment, "/")
	if !ok || namespace == "" || name == "" {
		return fmt.Errorf("-quiesce-deployment %q: want namespace/name", deployment)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	return app.QuiesceDeployment(ctx, clientset, namespace, name, 10*time.Minute)
}

func controllerAuthenticator(mode, userIDClaim string) (auth.AuthProvider, error) {
	switch mode {
	case "insecure":
		return &authimpl.InsecureAuthenticator{}, nil
	case "trusted-proxy":
		return authimpl.NewProxyAuthenticator(userIDClaim), nil
	default:
		return nil, fmt.Errorf("unsupported server.auth.mode %q", mode)
	}
}

func podTelemetryDefaults(cfg *configv1.ControllerConfig, getenv func(string) string) {
	if cfg.Telemetry.ResourceAttributes == nil {
		cfg.Telemetry.ResourceAttributes = map[string]string{}
	}
	for key, value := range map[string]string{
		"k8s.pod.name":        getenv("K8S_POD_NAME"),
		"k8s.pod.uid":         getenv("K8S_POD_UID"),
		"service.instance.id": getenv("K8S_POD_UID"),
		"k8s.node.name":       getenv("K8S_NODE_NAME"),
		"k8s.namespace.name":  cfg.GetKubernetes().GetNamespace(),
		"k8s.container.name":  "controller",
	} {
		if _, set := cfg.Telemetry.ResourceAttributes[key]; !set && value != "" {
			cfg.Telemetry.ResourceAttributes[key] = value
		}
	}
}
