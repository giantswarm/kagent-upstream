package image_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kagent-dev/kagent/go/core/internal/image"
)

func TestIsDigestPinned(t *testing.T) {
	const digest = "@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tt := range []struct {
		name      string
		reference string
		want      bool
	}{
		{"pinned", "registry/image" + digest, true},
		{"registry port", "localhost:5001/team/image" + digest, true},
		{"tag and digest", "registry/image:tag" + digest, true},
		{"empty", "", false},
		{"tag only", "registry/image:latest", false},
		{"missing repository", digest, false},
		{"short digest", "registry/image" + digest[:len(digest)-1], false},
		{"uppercase digest", "registry/image@sha256:" + strings.Repeat("A", 64), false},
		{"multiple digests", "registry/image" + digest + digest, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, image.IsDigestPinned(tt.reference))
		})
	}
	for _, whitespace := range " \t\n\r\v\f" {
		require.False(t, image.IsDigestPinned("registry/"+string(whitespace)+"image"+digest))
	}
}

func TestInRepositories(t *testing.T) {
	const digest = "@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	repositories := []string{"registry.example.com/tools", "localhost:5001/team/go/"}
	for _, tt := range []struct {
		name       string
		reference  string
		repository string
		want       bool
	}{
		{"the repository", "registry.example.com/tools" + digest, "registry.example.com/tools", true},
		{"below it", "registry.example.com/tools/go" + digest, "registry.example.com/tools/go", true},
		{"with a tag", "registry.example.com/tools/go:1.26" + digest, "registry.example.com/tools/go", true},
		{"registry port", "localhost:5001/team/go" + digest, "localhost:5001/team/go", true},
		{"a sibling with the prefix", "registry.example.com/tools-extra/go" + digest, "registry.example.com/tools-extra/go", false},
		{"a parent", "registry.example.com/go" + digest, "registry.example.com/go", false},
		{"another registry", "evil.example.com/tools/go" + digest, "evil.example.com/tools/go", false},
		{"a short name", "golang" + digest, "golang", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.repository, image.Repository(tt.reference))
			require.Equal(t, tt.want, image.InRepositories(tt.reference, repositories))
		})
	}
	require.False(t, image.InRepositories("registry.example.com/tools"+digest, nil))
	require.False(t, image.InRepositories("registry.example.com/tools"+digest, []string{""}))
}
