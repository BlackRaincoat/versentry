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
	// Bump is major|minor|patch for semver/numeric tag updates, and for digest
	// updates whose OCI versions both parse as semver; empty otherwise.
	Bump         string
	LocalDigest  string
	RemoteDigest string
	CheckedAt    time.Time
	// CurrentVersion / LatestVersion: org.opencontainers.image.version of the
	// running and the new image, for digest updates (a moved floating tag).
	// Empty when the image carries no such label or it was not looked up.
	CurrentVersion string
	LatestVersion  string
	// LatestCreated: build time of the new image; zero when unknown.
	LatestCreated time.Time
	// Reminder: same target notified before; re-sent because remind_after elapsed.
	Reminder bool
}

// Semver/numeric bump kinds for UpdateAvailable.Bump and notifier templates.
const (
	BumpMajor = "major"
	BumpMinor = "minor"
	BumpPatch = "patch"
)
