package work

import (
	"os"
	"path/filepath"
	"strings"
)

// Console battery fast-fail policy.
//
// A recorded 64-minute battery hang on 2026-08-14 was caused by a console/* proof step
// running under bun while (a) console/node_modules was absent and (b) the process
// inherited an HTTPS_PROXY pointing at a local loopback that no longer served traffic.
// bun's auto-install walked the proxy and blocked indefinitely on TCP CONNECT. The
// dispatcher must fail fast with a NAMED reason before any bun-dependent step is
// launched, never hang waiting on the auto-install. This file owns the named-reason
// policy; the dispatcher integration lives in cmd/compound.

// ConsoleBatteryFastFailReason reports the named reason that disqualifies a
// console/TypeScript proof step from being launched as-is, or "" when the step is not
// at risk.
//
// Recognised named reasons (stable strings the dispatcher can match on):
//
//	console_deps_missing  - step needs console/node_modules and the directory is absent
//	proxy_env_blocks_bun  - step uses bun and HTTPS_PROXY/https_proxy is inherited and
//	                        points at a loopback address (the model-gateway signature:
//	                        bun honours these vars for every network fetch and a loopback
//	                        proxy that no longer serving blocks indefinitely)
//
// repo is the resolved proof checkout root. step is the gate under test. The function
// is intentionally narrow: only console/* bun-driven gates are covered because that is
// the recorded hang signature. Non-console bun steps (e.g., bun-tools-test at the repo
// root) and non-bun console gates (console-tsc, console-mutation-*) either cannot
// auto-install (immediate ENOENT) or are not known to hang, and are out of scope.
func ConsoleBatteryFastFailReason(repo string, step ProofCheckStep) string {
	if !isBunConsoleStep(step) {
		return ""
	}
	if !hasConsoleNodeModules(repo) {
		return "console_deps_missing"
	}
	if InheritedLoopbackProxyReason() != "" {
		return "proxy_env_blocks_bun"
	}
	return ""
}

// isBunConsoleStep is true when a step is rooted in console/ and its command is
// dispatched under the bun runtime. Tsc-style gates are out of scope: their failure
// mode on missing node_modules is an immediate ENOENT, not a silent TCP hang.
func isBunConsoleStep(step ProofCheckStep) bool {
	if step.Dir != "console" {
		return false
	}
	return len(step.Command) > 0 && step.Command[0] == "bun"
}

// hasConsoleNodeModules reports whether console/node_modules exists as a directory
// in repo. A symlink whose target is missing satisfies IsDir=false and is treated as
// absent: the bun auto-install path that hung wants the real directory, not a dangling
// link.
func hasConsoleNodeModules(repo string) bool {
	if repo == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(repo, "console", "node_modules"))
	if err != nil || info == nil {
		return false
	}
	return info.IsDir()
}

// InheritedLoopbackProxyReason returns "proxy_env_blocks_bun" when HTTPS_PROXY or
// https_proxy is set on the current process AND the URL points at a loopback host
// with a non-empty port. Empty string means no inherited loopback proxy.
//
// The "host must include a port" rule excludes bare "http://localhost" entries from a
// malformed shell config: those resolve on the bun side without TCP indirection and
// do not produce the hang-class behavior.
func InheritedLoopbackProxyReason() string {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy"} {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		if reason := classifyLoopbackProxy(raw); reason != "" {
			return reason
		}
	}
	return ""
}

// classifyLoopbackProxy returns "proxy_env_blocks_bun" iff raw is a URL whose host is
// a loopback name (localhost, 127.x.x.x, ::1) and whose port is non-empty. Anything
// not in that shape returns "": extending the list requires another investigation, not
// a guess.
func classifyLoopbackProxy(raw string) string {
	url := raw
	if i := strings.Index(url, "://"); i >= 0 {
		url = url[i+3:]
	}
	if i := strings.Index(url, "/"); i >= 0 {
		url = url[:i]
	}
	if i := strings.Index(url, "@"); i >= 0 {
		url = url[i+1:]
	}
	colon := strings.LastIndex(url, ":")
	if colon < 0 {
		return ""
	}
	hostPart := url[:colon]
	portPart := strings.TrimSpace(url[colon+1:])
	if portPart == "" {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(hostPart))
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.") {
		return "proxy_env_blocks_bun"
	}
	return ""
}
