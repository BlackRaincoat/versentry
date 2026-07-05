package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/registry"
	_ "github.com/BlackRaincoat/versentry/internal/registry/oci"
)

func TestDockerHubNormalizationViaOCI(t *testing.T) {
	cases := []struct {
		raw  string
		host string
		repo string
	}{
		{"nginx:latest", "index.docker.io", "library/nginx"},
		{"library/nginx:1.25", "index.docker.io", "library/nginx"},
		{"docker.io/library/redis:7", "index.docker.io", "library/redis"},
	}
	for _, tc := range cases {
		p, err := imageref.Parse(tc.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.raw, err)
		}
		if p.Host != tc.host || p.Repo != tc.repo {
			t.Fatalf("parse %q: got host=%q repo=%q, want host=%q repo=%q",
				tc.raw, p.Host, p.Repo, tc.host, tc.repo)
		}
	}
}

func TestDockerHubListAndDigestViaOCI(t *testing.T) {
	reg, err := registry.New("oci", map[string]any{"host": "index.docker.io"})
	if err != nil {
		t.Fatal(err)
	}
	if reg.Host() != "index.docker.io" {
		t.Fatalf("host = %q", reg.Host())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	tags, err := reg.ListTags(ctx, "library/nginx")
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) == 0 {
		t.Fatal("ListTags returned no tags")
	}

	dig, err := reg.TagDigest(ctx, "library/nginx", "1.25")
	if err != nil {
		t.Fatalf("TagDigest: %v", err)
	}
	if dig == "" {
		t.Fatal("empty digest")
	}
}
