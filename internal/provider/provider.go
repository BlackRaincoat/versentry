package provider

import (
	"context"
	"fmt"

	"github.com/BlackRaincoat/versentry/internal/model"
)

// Provider lists running containers and resolves the local digest for a container image.
type Provider interface {
	ListRunning(ctx context.Context) ([]model.Container, error)
	LocalDigest(ctx context.Context, container model.Container, repo string) (string, error)
	// Ping checks API reachability without listing or inspecting workloads (for health probes).
	Ping(ctx context.Context) error
}

// Factory constructs a Provider from plugin-specific configuration.
type Factory func(cfg map[string]any) (Provider, error)

var factories = map[string]Factory{}

// Register adds a Provider factory. Called from plugin init() functions.
func Register(name string, factory Factory) {
	factories[name] = factory
}

// New instantiates a Provider by registered name.
func New(name string, cfg map[string]any) (Provider, error) {
	factory, ok := factories[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", name)
	}
	return factory(cfg)
}
