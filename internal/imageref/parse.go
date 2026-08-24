package imageref

import (
	"fmt"
	"strings"
	"unicode"

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

	// Docker Image for a container started by ID is "sha256:<64 hex>" with no
	// repository. go-containerregistry treats that as Hub library/sha256:<hex>.
	if digest, ok := bareDigestRef(raw); ok {
		return Parsed{Digest: digest}, nil
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

// bareDigestRef reports a Docker image ID with no repository name
// (sha256:<64 hex>). Named pins like nginx@sha256:<hex> are not bare.
func bareDigestRef(raw string) (string, bool) {
	const prefix = "sha256:"
	if len(raw) != len(prefix)+64 {
		return "", false
	}
	if !strings.EqualFold(raw[:len(prefix)], prefix) {
		return "", false
	}
	hex := raw[len(prefix):]
	for _, r := range hex {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return "", false
		}
	}
	return prefix + strings.ToLower(hex), true
}
