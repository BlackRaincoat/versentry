package version

// Set at build time via -ldflags.
var (
	Version = "dev"
	Commit  = ""
)

// String returns the version string for CLI output.
func String() string {
	if Commit != "" {
		return Version + " (" + Commit + ")"
	}
	return Version
}
