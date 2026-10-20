// Copyright 2026 The kagent Authors
// SPDX-License-Identifier: Apache-2.0

package adk

import (
	"path"
	"strings"
)

// ValidRepository reports whether repository is <owner>/<repository>, the
// only form a ChangedRepositoriesHeader entry takes: one clean path segment
// each, neither . nor .., so it names a clone directly under the working
// directory. Whoever admits a session's changed repositories refuses what a
// runtime would not fetch.
func ValidRepository(repository string) bool {
	owner, name, ok := strings.Cut(repository, "/")
	return ok && owner != "" && name != "" && !strings.Contains(name, "/") && path.Clean(repository) == repository &&
		owner != ".." && name != ".." && owner != "." && name != "."
}
