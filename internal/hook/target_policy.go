package hook

import (
	"net/url"
	"path/filepath"
	"strings"
)

const NonLocalTargetSecurityReason = "non-local target URI requires explicit external authority"

// LocalTargetPath classifies a target before any filepath operation. Plain
// paths preserve the existing absolute/relative behavior. file:// is accepted
// only for an unambiguous local absolute path; every other URI remains a
// non-local locator and can never enter path-scoped policy or git inspection.
func LocalTargetPath(raw, cwd string) (string, bool) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", true
	}
	if !strings.Contains(target, "://") {
		if filepath.IsAbs(target) {
			return filepath.Clean(target), true
		}
		cwd = strings.TrimSpace(cwd)
		if cwd == "" {
			return target, true
		}
		return filepath.Join(cwd, target), true
	}

	parsed, err := url.Parse(target)
	if err != nil || !strings.EqualFold(parsed.Scheme, "file") {
		return "", false
	}
	if parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host != "" && !strings.EqualFold(host, "localhost") {
		return "", false
	}
	if parsed.Port() != "" || !filepath.IsAbs(parsed.Path) {
		return "", false
	}
	return filepath.Clean(filepath.FromSlash(parsed.Path)), true
}
