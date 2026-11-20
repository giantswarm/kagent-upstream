package turn

import (
	"slices"
	"strings"
)

// environmentNames are the variables a model-run command inherits from the
// runtime: what a shell needs to find programs, a home, a locale and a
// temporary directory, and the CA bundles a command needs to reach the
// network through a TLS-intercepting egress proxy. Everything else stays in
// the runtime -- provider API keys, tokens, and the KAGENT_* configuration --
// because the model writes the command and reads its output.
//
// An allowlist rather than a pattern of secret-looking names: a credential
// whose name matches no pattern (a password in a database URL, a JSON config)
// is withheld too, and a new runtime variable is withheld until it is listed
// here. Proxy variables are left out on purpose: their URLs may carry
// credentials.
var environmentNames = map[string]struct{}{
	"PATH":    {},
	"HOME":    {},
	"USER":    {},
	"LOGNAME": {},
	"LANG":    {},
	"TERM":    {},
	"TZ":      {},
	"TMPDIR":  {},

	"SSL_CERT_FILE":       {},
	"SSL_CERT_DIR":        {},
	"CURL_CA_BUNDLE":      {},
	"REQUESTS_CA_BUNDLE":  {},
	"GIT_SSL_CAINFO":      {},
	"NODE_EXTRA_CA_CERTS": {},
	"AWS_CA_BUNDLE":       {},
}

// CommandEnvironment builds the environment of a command a turn runs, a
// model-run command or the git of the session directory's setup, from
// environ, keeping only environmentNames and the LC_* locale variables.
// configured supplies literal values independently of the runtime environment.
// PWD always names the working directory. A nil cmd.Env inherits everything,
// so the result is always non-nil.
func CommandEnvironment(environ []string, workingDir string, configured map[string]string) []string {
	env := make([]string, 0, len(environmentNames)+1)
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		_, overridden := configured[name]
		if _, ok := environmentNames[name]; !overridden && (ok || strings.HasPrefix(name, "LC_")) {
			env = append(env, entry)
		}
	}
	names := make([]string, 0, len(configured))
	for name := range configured {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if name != "PWD" {
			env = append(env, name+"="+configured[name])
		}
	}
	return append(env, "PWD="+workingDir)
}
