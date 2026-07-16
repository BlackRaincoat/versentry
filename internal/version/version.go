package version

import "log/slog"

// Set at build time via -ldflags (Dockerfile ARG VERSION/COMMIT).
// Defaults match a non-release local build (go build / docker without args).
var (
	Version = "dev"
	Commit  = "unknown"
)

// String returns the version string for CLI output.
func String() string {
	v := DisplayVersion()
	c := ShortCommit()
	if c == "unknown" {
		return v
	}
	return v + " (" + c + ")"
}

// DisplayVersion returns a non-empty version label for logs and CLI.
func DisplayVersion() string {
	if Version == "" {
		return "dev"
	}
	return Version
}

// ShortCommit returns a short commit id for logs (8 hex chars when given a full SHA).
func ShortCommit() string {
	c := Commit
	if c == "" {
		return "unknown"
	}
	if len(c) > 8 {
		return c[:8]
	}
	return c
}

// LogStartup writes binary identity from ldflags (not OCI image labels).
func LogStartup(log *slog.Logger) {
	if log == nil {
		return
	}
	log.Info("versentry starting",
		"version", DisplayVersion(),
		"commit", ShortCommit(),
	)
}
