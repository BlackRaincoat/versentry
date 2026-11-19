package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/provider"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/moby/moby/client"
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

	cli, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	return &Provider{client: cli}, nil
}

// Ping checks that the Docker API is reachable (lightweight health probe).
func (p *Provider) Ping(ctx context.Context) error {
	if _, err := p.client.Ping(ctx, client.PingOptions{}); err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	return nil
}

// ListRunning returns all currently running containers.
// Labels merge image OCI labels with container labels (container wins on key clash).
func (p *Provider) ListRunning(ctx context.Context) ([]model.Container, error) {
	result, err := p.client.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	imageLabels := make(map[string]map[string]string)
	out := make([]model.Container, 0, len(result.Items))
	for _, c := range result.Items {
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
	var imgLabels map[string]string
	if imageID != "" {
		var ok bool
		imgLabels, ok = cache[imageID]
		if !ok {
			imgLabels = map[string]string{}
			if img, err := p.client.ImageInspect(ctx, imageID); err == nil && img.Config != nil {
				for k, v := range img.Config.Labels {
					imgLabels[k] = v
				}
			}
			cache[imageID] = imgLabels
		}
	}
	return mergeLabels(imgLabels, containerLabels)
}

// controlLabelPrefix marks Versentry's own control labels (watch, include,
// track, min_age). They override central config, so only values set when the
// container was created (compose / docker run) count — never ones an image
// publisher baked in with Dockerfile LABEL.
const controlLabelPrefix = "versentry."

// mergeLabels returns image labels overlaid by container labels (container wins).
// Docker copies image labels into the container's own labels, so a versentry.*
// container label whose value equals the image's is treated as image-provided
// and dropped. Known limit: if an image bakes versentry.X=v, a container label
// with the same v cannot be told apart and is dropped too — use the config
// (rules[], exclude_containers, watch_containers) for that image instead.
// Without image labels (inspect failed) nothing is dropped.
func mergeLabels(imgLabels, containerLabels map[string]string) map[string]string {
	labels := make(map[string]string, len(imgLabels)+len(containerLabels))
	for k, v := range imgLabels {
		if strings.HasPrefix(k, controlLabelPrefix) {
			continue
		}
		labels[k] = v
	}
	for k, v := range containerLabels {
		if strings.HasPrefix(k, controlLabelPrefix) {
			if iv, fromImage := imgLabels[k]; fromImage && iv == v {
				continue
			}
		}
		labels[k] = v
	}
	return labels
}

// LocalDigest returns the repo digest of the image currently used by the container,
// taken from the RepoDigests entry for host/repo. The host must match too: an
// image pulled from both docker.io/acme/app and ghcr.io/acme/app carries one
// entry per registry, and their digests can differ.
func (p *Provider) LocalDigest(ctx context.Context, c model.Container, host, repo string) (string, error) {
	inspect, err := p.client.ContainerInspect(ctx, c.ID, client.ContainerInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspect container %s: %w", c.ID, err)
	}

	img, err := p.client.ImageInspect(ctx, inspect.Container.Image)
	if err != nil {
		return "", fmt.Errorf("inspect image for container %s: %w", c.Name, err)
	}

	for _, repoDigest := range img.RepoDigests {
		ref, err := name.ParseReference(repoDigest)
		if err != nil {
			continue
		}
		if ref.Context().RegistryStr() == host && ref.Context().RepositoryStr() == repo {
			if d, ok := ref.(name.Digest); ok {
				return d.DigestStr(), nil
			}
		}
	}

	// No registry manifest digest in RepoDigests (locally built or not pulled from a registry).
	// Do not fall back to image ID — it is not comparable to a registry tag digest.
	return "", fmt.Errorf("no registry digest for %s/%s on container %s", host, repo, c.Name)
}
