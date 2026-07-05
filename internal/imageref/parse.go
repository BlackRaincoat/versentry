package imageref

import (
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
)

// Parsed holds the normalized components of a container image reference.
type Parsed struct {
	Host   string // registry host, e.g. index.docker.io
	Repo   string // repository path, e.g. library/nginx
	Tag    string // empty when the reference is digest-only
	Digest string // set when the reference is digest-pinned
}

// Parse normalizes a raw image reference using go-containerregistry/name.
func Parse(raw string) (Parsed, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Parsed{}, fmt.Errorf("empty image reference")
	}

	ref, err := name.ParseReference(raw, name.WeakValidation)
	if err != nil {
		return Parsed{}, fmt.Errorf("parse image reference %q: %w", raw, err)
	}

	ctx := ref.Context()
	parsed := Parsed{
		Host: ctx.RegistryStr(),
		Repo: ctx.RepositoryStr(),
	}

	switch r := ref.(type) {
	case name.Tag:
		parsed.Tag = r.TagStr()
	case name.Digest:
		parsed.Digest = r.DigestStr()
	default:
		return Parsed{}, fmt.Errorf("unsupported reference type for %q", raw)
	}

	return parsed, nil
}
