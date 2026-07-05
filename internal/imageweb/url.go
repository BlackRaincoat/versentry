package imageweb

import (
	"fmt"
	"net/url"
	"strings"
)

const labelSource = "org.opencontainers.image.source"

// URL builds a human-facing web link for an image update.
// Prefer GitHub releases when org.opencontainers.image.source points at github.com;
// otherwise fall back to a known registry page. Returns "" when no reliable URL exists.
func URL(host, repo, tag string, labels map[string]string) string {
	if u := githubReleaseURL(labels, tag); u != "" {
		return u
	}
	return registryPageURL(host, repo, tag)
}

func githubReleaseURL(labels map[string]string, tag string) string {
	if tag == "" || labels == nil {
		return ""
	}
	source := strings.TrimSpace(labels[labelSource])
	if source == "" {
		return ""
	}

	u, err := url.Parse(source)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return ""
	}

	path := strings.Trim(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}

	// owner/repo only — ignore extra path segments.
	base := fmt.Sprintf("https://github.com/%s/%s", parts[0], parts[1])
	return base + "/releases/tag/" + url.PathEscape(tag)
}

func registryPageURL(host, repo, tag string) string {
	switch host {
	case "index.docker.io":
		return dockerHubURL(repo, tag)
	case "quay.io":
		if repo == "" {
			return ""
		}
		return "https://quay.io/repository/" + repo + "?tab=tags"
	default:
		// GHCR, GitLab, and unknown/self-hosted hosts have no reliable public page scheme.
		return ""
	}
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
