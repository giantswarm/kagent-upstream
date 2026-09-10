package skillsinit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitCredentialFromEnvironmentIsKeyedByHost(t *testing.T) {
	t.Setenv("KAGENT_GIT_TOKEN_GITHUB_COM", "ghp_value")
	t.Setenv("KAGENT_GIT_TOKEN_GIT_EXAMPLE_COM_8443", "other")
	t.Setenv("KAGENT_GIT_USERNAME_GIT_EXAMPLE_COM_8443", "deploy-token-1")

	github := GitCredentialFromEnvironment("https://github.com/acme/private-skills")
	require.NotNil(t, github)
	assert.Equal(t, GitCredential{TokenEnv: "KAGENT_GIT_TOKEN_GITHUB_COM"}, *github)

	example := GitCredentialFromEnvironment("https://git.example.com:8443/group/skills.git")
	require.NotNil(t, example)
	assert.Equal(t, GitCredential{Username: "deploy-token-1", TokenEnv: "KAGENT_GIT_TOKEN_GIT_EXAMPLE_COM_8443"}, *example)

	assert.Nil(t, GitCredentialFromEnvironment("https://gitlab.com/group/skills"), "a host without a token fetches anonymously")
	assert.Nil(t, GitCredentialFromEnvironment("not a url"))
}

func TestGitCredentialFromEnvironmentIgnoresAnEmptyToken(t *testing.T) {
	t.Setenv("KAGENT_GIT_TOKEN_GITHUB_COM", "")
	assert.Nil(t, GitCredentialFromEnvironment("https://github.com/acme/private-skills"))
}
