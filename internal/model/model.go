package model

import "time"

// Container is a running workload reported by a Provider.
// ImageRef is kept raw; parsing is done by the core via imageref.Parse.
type Container struct {
	ID       string
	Name     string
	ImageRef string
	Labels   map[string]string
}

// UpdateAvailable is published when a newer image version is available.
type UpdateAvailable struct {
	Container  Container
	Host       string
	Repo       string
	CurrentTag string
	LatestTag  string
	// Bump is major|minor|patch for semver/numeric tag updates; empty for digest.
	Bump         string
	LocalDigest  string
	RemoteDigest string
	CheckedAt    time.Time
}

// Semver/numeric bump kinds for UpdateAvailable.Bump and notifier templates.
const (
	BumpMajor = "major"
	BumpMinor = "minor"
	BumpPatch = "patch"
)
