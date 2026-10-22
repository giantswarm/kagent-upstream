package image

import (
	"regexp"
	"strings"
)

var digestPinned = regexp.MustCompile(`^[^[:space:]@]+@sha256:[a-f0-9]{64}$`)

// IsDigestPinned reports whether reference names an image pinned by SHA-256 digest.
func IsDigestPinned(reference string) bool {
	return digestPinned.MatchString(reference)
}

// Repository returns reference without its digest and tag, as written: a short
// Docker Hub name is not expanded.
func Repository(reference string) string {
	repository, _, _ := strings.Cut(reference, "@")
	if colon := strings.LastIndex(repository, ":"); colon > strings.LastIndex(repository, "/") {
		repository = repository[:colon]
	}
	return repository
}

// InRepositories reports whether reference comes from one of repositories or
// from a repository below one of them. Matching is by path segment, so
// "registry.example.com/tools" admits "registry.example.com/tools/go" but not
// "registry.example.com/tools-extra".
func InRepositories(reference string, repositories []string) bool {
	repository := Repository(reference)
	for _, allowed := range repositories {
		allowed = strings.TrimSuffix(allowed, "/")
		if allowed != "" && (repository == allowed || strings.HasPrefix(repository, allowed+"/")) {
			return true
		}
	}
	return false
}
