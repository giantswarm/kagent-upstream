package config_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	configv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/config/v1"
	"github.com/kagent-dev/kagent/go/core/pkg/config"
)

func TestHelmControllerConfig(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("Helm is required for chart/config integration")
	}
	// Use the real templates without unrelated subcharts or generated chart metadata.
	chart := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(chart, "templates"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("apiVersion: v2\nname: kagent\nversion: 0.0.0\n"), 0o600))
	for _, file := range []string{"values.yaml", "templates/_helpers.tpl", "templates/_images.tpl", "templates/controller-configmap.yaml", "templates/controller-deployment.yaml"} {
		data, err := os.ReadFile(filepath.Join("../../../../helm/kagent", file))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(chart, file), data, 0o600))
	}
	render := func(values string) (*configv1.ControllerConfig, string, error) {
		cmd := exec.CommandContext(t.Context(), "helm", "template", "demo", chart, "--namespace", "team", "-f", "-")
		cmd.Stdin = strings.NewReader(values)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		decoder := yaml.NewDecoder(strings.NewReader(string(output)))
		cfg := &configv1.ControllerConfig{}
		checksum := ""
		for {
			var object struct {
				Kind string            `yaml:"kind"`
				Data map[string]string `yaml:"data"`
				Spec struct {
					Template struct {
						Metadata struct {
							Annotations map[string]string `yaml:"annotations"`
						} `yaml:"metadata"`
					} `yaml:"template"`
				} `yaml:"spec"`
			}
			err := decoder.Decode(&object)
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			if object.Kind == "ConfigMap" {
				if err := config.Decode([]byte(object.Data["controller.yaml"]), cfg); err != nil {
					return nil, "", err
				}
			}
			if object.Kind == "Deployment" {
				checksum = object.Spec.Template.Metadata.Annotations["checksum/configmap"]
			}
		}
		config.DefaultController(cfg)
		return cfg, checksum, config.Validate(cfg)
	}
	base, checksum, err := render("{}")
	require.NoError(t, err)
	require.Equal(t, "team", base.Kubernetes.GetNamespace())
	require.Equal(t, "http://demo-controller.team.svc.cluster.local:8083", base.RuntimeDefaults.GetControlPlaneUrl())
	require.NotEmpty(t, checksum)
	require.Equal(t, "kagent", base.Database.GetSchema())
	require.Equal(t, "extensions", base.Database.GetVectorSchema())
	require.False(t, base.GetDatabase().GetSkipMigrations())
	require.Empty(t, base.Push.SigningPrivateKeyFile)
	verify, verifyChecksum, err := render("config:\n  database: {skipMigrations: true}\n")
	require.NoError(t, err)
	require.True(t, verify.GetDatabase().GetSkipMigrations())
	require.NotEqual(t, checksum, verifyChecksum)
	signed, signedChecksum, err := render("controller:\n  push:\n    signing: {secretName: receiver-key}\nconfig:\n  database: {schema: tenant, vectorSchema: public}\n")
	require.NoError(t, err)
	require.Equal(t, "/etc/kagent/secrets/push-private-key.pem", signed.Push.SigningPrivateKeyFile)
	require.Equal(t, "tenant", signed.Database.GetSchema())
	require.Equal(t, "public", signed.Database.GetVectorSchema())
	require.NotEqual(t, checksum, signedChecksum)
	changed, changedChecksum, err := render("config:\n  sessions: {idleTtl: 0s}\n  kubernetes: {leaderElection: false}\n  telemetry:\n    traces: {endpoint: 'http://collector:4317'}\n")
	require.NoError(t, err)
	require.Zero(t, changed.Sessions.IdleTtl.AsDuration())
	require.False(t, changed.Kubernetes.GetLeaderElection())
	require.Equal(t, "grpc", changed.Telemetry.Traces.GetProtocol())
	require.Nil(t, changed.RuntimeDefaults.Telemetry.Traces)
	require.NotEqual(t, checksum, changedChecksum)
	images, imagesChecksum, err := render("global: {imageRegistry: mirror.example}\nconfig:\n  harnessImages:\n    release: 1.0.0-alpha9\n    codex: ghcr.io/kagent-dev/kagent/codex-harness@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n")
	require.NoError(t, err)
	require.Equal(t, "1.0.0-alpha9", images.HarnessImages.Release)
	require.Equal(t, "mirror.example/kagent-dev/kagent/codex-harness@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", images.HarnessImages.Codex)
	require.Empty(t, images.HarnessImages.Kagent)
	require.NotEqual(t, checksum, imagesChecksum)
	push, pushChecksum, err := render("config:\n  push: {issuer: https://issuer.example, recoveryInterval: 15s, allowHttp: true, allowPrivateNetworks: true}\n")
	require.NoError(t, err)
	require.Equal(t, "https://issuer.example", push.Push.GetIssuer())
	require.Equal(t, 15*time.Second, push.Push.RecoveryInterval.AsDuration())
	require.True(t, push.Push.AllowHttp)
	require.True(t, push.Push.AllowPrivateNetworks)
	require.NotEqual(t, checksum, pushChecksum)
	require.Nil(t, changed.Sessions.Admission)
	admitted, _, err := render("config:\n  sessions:\n    admission:\n      jwt: {jwksUrl: 'https://issuer.example.test/jwks.json', issuer: 'https://workspaces.example.test', audience: kagent, claims: {volume: workspace_volume, agentNamespace: workspace_agent_namespace, egress: workspace_egress}, credentialAuthority: 'ate-secret://credentials.example.test/workspaces'}\n")
	require.NoError(t, err)
	require.Equal(t, "https://issuer.example.test/jwks.json", admitted.Sessions.Admission.Jwt.GetJwksUrl())
	require.Equal(t, "kagent", admitted.Sessions.Admission.Jwt.GetAudience())
	require.Equal(t, "workspace_volume", admitted.Sessions.Admission.Jwt.Claims.GetVolume())
	require.Equal(t, "workspace_agent_namespace", admitted.Sessions.Admission.Jwt.Claims.GetAgentNamespace())
	require.Equal(t, "workspace_egress", admitted.Sessions.Admission.Jwt.Claims.GetEgress())
	require.Equal(t, "ate-secret://credentials.example.test/workspaces", admitted.Sessions.Admission.Jwt.GetCredentialAuthority())
	_, _, err = render("config:\n  sessions:\n    admission:\n      jwt: {jwksUrl: 'https://issuer.example.test/jwks.json', issuer: 'https://workspaces.example.test', audience: kagent, credentialAuthority: 'https://credentials.example.test/workspaces'}\n")
	require.ErrorContains(t, err, "credential_authority")
	require.Equal(t, 604800.0, admitted.Sessions.IdleTtl.AsDuration().Seconds())
	_, _, err = render("config:\n  sessions:\n    admission:\n      jwt: {issuer: 'https://workspaces.example.test', audience: kagent}\n")
	require.ErrorContains(t, err, "jwks_url")
	// A plain http jwksUrl without allowHttp is a valid config: the controller
	// logs it and refuses every volume source rather than failing to start.
	plain, _, err := render("config:\n  sessions:\n    admission:\n      jwt: {jwksUrl: 'http://issuer.team.svc/jwks.json', issuer: 'https://workspaces.example.test', audience: kagent}\n")
	require.NoError(t, err)
	require.False(t, plain.Sessions.Admission.Jwt.GetAllowHttp())
	inCluster, _, err := render("config:\n  sessions:\n    admission:\n      jwt: {jwksUrl: 'http://issuer.team.svc/jwks.json', allowHttp: true, issuer: 'https://workspaces.example.test', audience: kagent}\n")
	require.NoError(t, err)
	require.True(t, inCluster.Sessions.Admission.Jwt.GetAllowHttp())
	_, _, err = render("config:\n  loging: {level: debug}\n")
	require.ErrorContains(t, err, "unknown field")
}
