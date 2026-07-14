package imageweb

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	labelSource = "org.opencontainers.image.source"

	// ModeSemver and ModeDigest match format.TrackingMode values.
	ModeSemver = "semver"
	ModeDigest = "digest"
)

// URL builds a human-facing web link for an image update.
// Prefer reliable pages (release lists, registry tag views) over guessed per-tag
// GitHub release URLs that often 404. Returns "" when no reliable URL exists.
//
// mode is "semver" or "digest" (see ModeSemver / ModeDigest).
// tag is LatestTag for semver updates and CurrentTag for digest updates.
func URL(host, repo, tag string, labels map[string]string, mode string) string {
	source := parseSource(labels)

	if mode == ModeSemver {
		if source != nil && isGitHub(source.host) {
			return source.repoURL + "/releases"
		}
		if u := registryPageURL(host, repo, tag, source); u != "" {
			return u
		}
		return sourceRepoURL(source)
	}

	// Digest: registry first (never git releases); then source repo; then empty.
	if u := registryPageURL(host, repo, tag, source); u != "" {
		return u
	}
	return sourceRepoURL(source)
}

type parsedSource struct {
	host    string
	repoURL string // https://host/owner/repo (no trailing slash)
}

func parseSource(labels map[string]string) *parsedSource {
	if labels == nil {
		return nil
	}
	raw := strings.TrimSpace(labels[labelSource])
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil
	}
	path := strings.Trim(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return &parsedSource{
		host:    host,
		repoURL: fmt.Sprintf("%s://%s/%s/%s", scheme, u.Hostname(), parts[0], parts[1]),
	}
}

func isGitHub(host string) bool {
	return host == "github.com" || host == "www.github.com"
}

func sourceRepoURL(source *parsedSource) string {
	if source == nil {
		return ""
	}
	return source.repoURL
}

func registryPageURL(host, repo, tag string, source *parsedSource) string {
	switch host {
	case "index.docker.io":
		return dockerHubURL(repo, tag)
	case "quay.io":
		if repo == "" {
			return ""
		}
		return "https://quay.io/repository/" + repo + "?tab=tags"
	case "ghcr.io":
		return ghcrPackageURL(repo, source)
	default:
		return ""
	}
}

// ghcrPackageURL builds github.com/{owner}/{repo}/pkgs/container/{package}
// only when a GitHub source label is present (package name = last OCI path segment).
func ghcrPackageURL(repo string, source *parsedSource) string {
	if source == nil || !isGitHub(source.host) {
		return ""
	}
	pkg := lastPathSegment(repo)
	if pkg == "" {
		return ""
	}
	return source.repoURL + "/pkgs/container/" + url.PathEscape(pkg)
}

func lastPathSegment(repo string) string {
	repo = strings.Trim(repo, "/")
	if repo == "" {
		return ""
	}
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

func dockerHubURL(repo, tag string) string {
	if repo == "" {
		return ""
	}

	var page string
	if name, ok := strings.CutPrefix(repo, "library/"); ok && name != "" && !strings.Contains(name, "/") {
		page = "https://hub.docker.com/_/" + name
	} else {
		page = "https://hub.docker.com/r/" + repo
	}

	if tag == "" {
		return page
	}
	return page + "?tag=" + url.QueryEscape(tag)
}
