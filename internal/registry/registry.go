package registry

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrNotFound is returned when a repository does not exist in the registry.
	ErrNotFound = errors.New("registry: repository not found")
	// ErrUnauthorized is returned when registry access is denied.
	ErrUnauthorized = errors.New("registry: unauthorized")
)

// Registry resolves tag digests and lists tags for a single registry host.
type Registry interface {
	Host() string
	TagDigest(ctx context.Context, repo, tag string) (digest string, err error)
	ListTags(ctx context.Context, repo string) ([]string, error)
}

// Factory constructs a Registry from plugin-specific configuration.
type Factory func(cfg map[string]any) (Registry, error)

var factories = map[string]Factory{}

// Register adds a Registry factory. Called from plugin init() functions.
func Register(name string, factory Factory) {
	factories[name] = factory
}

// New instantiates a Registry by registered name.
func New(name string, cfg map[string]any) (Registry, error) {
	factory, ok := factories[name]
	if !ok {
		return nil, fmt.Errorf("unknown registry %q", name)
	}
	return factory(cfg)
}
