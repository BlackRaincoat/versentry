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
	Container    Container
	Host         string
	Repo         string
	CurrentTag   string
	LatestTag    string
	LocalDigest  string
	RemoteDigest string
	CheckedAt    time.Time
}
