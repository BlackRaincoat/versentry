package notifier

import (
	"context"
	"fmt"

	"github.com/BlackRaincoat/versentry/internal/model"
)

// Notifier delivers all updates found in one check pass.
// Callers must not invoke Notify with an empty slice.
type Notifier interface {
	Notify(ctx context.Context, events []model.UpdateAvailable) error
}

// Factory constructs a Notifier from plugin-specific configuration.
type Factory func(cfg map[string]any) (Notifier, error)

var factories = map[string]Factory{}

// Register adds a Notifier factory. Called from plugin init() functions.
func Register(name string, factory Factory) {
	factories[name] = factory
}

// New instantiates a Notifier by registered name.
func New(name string, cfg map[string]any) (Notifier, error) {
	factory, ok := factories[name]
	if !ok {
		return nil, fmt.Errorf("unknown notifier %q", name)
	}
	return factory(cfg)
}
