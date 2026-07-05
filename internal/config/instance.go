package config

import (
	"os"
	"strings"
)

const (
	hostNameFile = "/etc/versentry/hostname"
)

// ResolveInstanceName picks a display name for notifications.
// Priority: instance_name after ApplyEnvOverrides (env overrides YAML)
// → /etc/versentry/hostname (optional bind mount from host) → os.Hostname()
// → "versentry" when the kernel hostname looks like a Docker container id.
func ResolveInstanceName(configured string) string {
	if name := strings.TrimSpace(configured); name != "" {
		return name
	}
	if name := readHostnameFile(hostNameFile); name != "" && !looksLikeContainerID(name) {
		return name
	}
	if host, err := os.Hostname(); err == nil {
		host = strings.TrimSpace(host)
		if host != "" && !looksLikeContainerID(host) {
			return host
		}
	}
	return "versentry"
}

func readHostnameFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// looksLikeContainerID reports short Docker container ids (12 hex chars).
func looksLikeContainerID(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
