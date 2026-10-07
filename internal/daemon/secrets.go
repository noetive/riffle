package daemon

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// MinSecret is the shortest value accepted as a secret.
const MinSecret = 4

// EnvSecrets resolves $secret:name from the daemon's environment. A secret is
// RIFFLE_SECRET_<NAME> and is bound to the origin in RIFFLE_SECRET_<NAME>_ORIGIN;
// without a bound origin it is unusable, so injected instructions cannot
// carry it to another site.
type EnvSecrets struct{}

// Resolve returns the secret value if the page origin is the bound origin.
func (EnvSecrets) Resolve(name, origin string) (string, error) {
	key := "RIFFLE_SECRET_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
	val, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("no secret %q is registered; the operator registers it as %s with %s_ORIGIN", name, key, key)
	}
	bound := os.Getenv(key + "_ORIGIN")
	if bound == "" {
		return "", fmt.Errorf("secret %q has no bound origin; the operator sets %s_ORIGIN", name, key)
	}
	// Masking a shorter value would hide it in ordinary text and say what it is.
	if utf8.RuneCountInString(val) < MinSecret {
		return "", fmt.Errorf("secret %q is shorter than %d characters, too short to hide in replies; the operator registers a longer one", name, MinSecret)
	}
	if bound != origin {
		return "", fmt.Errorf("secret %q is bound to %s and cannot be used on %s", name, bound, origin)
	}
	return val, nil
}
