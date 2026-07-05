package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/provider"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/name"
)

func init() {
	provider.Register("docker", New)
}

// Provider reads running containers via the local Docker socket.
type Provider struct {
	client *client.Client
}

// New constructs a Docker socket Provider.
func New(cfg map[string]any) (provider.Provider, error) {
	opts := []client.Opt{
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	}
	if socket, ok := cfg["socket"].(string); ok && socket != "" {
		opts = append(opts, client.WithHost("unix://"+socket))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	return &Provider{client: cli}, nil
}

// Ping checks that the Docker API is reachable (lightweight health probe).
func (p *Provider) Ping(ctx context.Context) error {
	if _, err := p.client.Ping(ctx); err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	return nil
}

// ListRunning returns all currently running containers.
// Labels merge image OCI labels with container labels (container wins on key clash).
func (p *Provider) ListRunning(ctx context.Context) ([]model.Container, error) {
	containers, err := p.client.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	imageLabels := make(map[string]map[string]string)
	out := make([]model.Container, 0, len(containers))
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		labels := p.labelsForContainer(ctx, c.ImageID, c.Labels, imageLabels)

		out = append(out, model.Container{
			ID:       c.ID,
			Name:     name,
			ImageRef: c.Image,
			Labels:   labels,
		})
	}

	return out, nil
}

func (p *Provider) labelsForContainer(
	ctx context.Context,
	imageID string,
	containerLabels map[string]string,
	cache map[string]map[string]string,
) map[string]string {
	labels := map[string]string{}

	if imageID != "" {
		imgLabels, ok := cache[imageID]
		if !ok {
			imgLabels = map[string]string{}
			if img, _, err := p.client.ImageInspectWithRaw(ctx, imageID); err == nil && img.Config != nil {
				for k, v := range img.Config.Labels {
					imgLabels[k] = v
				}
			}
			cache[imageID] = imgLabels
		}
		for k, v := range imgLabels {
			labels[k] = v
		}
	}

	for k, v := range containerLabels {
		labels[k] = v
	}
	return labels
}

// LocalDigest returns the repo digest of the image currently used by the container.
func (p *Provider) LocalDigest(ctx context.Context, c model.Container, repo string) (string, error) {
	inspect, err := p.client.ContainerInspect(ctx, c.ID)
	if err != nil {
		return "", fmt.Errorf("inspect container %s: %w", c.ID, err)
	}

	img, _, err := p.client.ImageInspectWithRaw(ctx, inspect.Image)
	if err != nil {
		return "", fmt.Errorf("inspect image for container %s: %w", c.Name, err)
	}

	for _, repoDigest := range img.RepoDigests {
		ref, err := name.ParseReference(repoDigest)
		if err != nil {
			continue
		}
		if ref.Context().RepositoryStr() == repo {
			if d, ok := ref.(name.Digest); ok {
				return d.DigestStr(), nil
			}
		}
	}

	// No registry manifest digest in RepoDigests (locally built or not pulled from a registry).
	// Do not fall back to image ID — it is not comparable to a registry tag digest.
	return "", fmt.Errorf("no registry digest for repo %q on container %s", repo, c.Name)
}
