package imageref

import (
	"strings"
	"testing"
)

const sampleDigest = "sha256:e0e67ea9c4bb0000000000000000000000000000000000000000000000000000"

func TestParseBareDigestIsNotLibrarySha256(t *testing.T) {
	p, err := Parse(sampleDigest)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Repo == "library/sha256" || p.Tag != "" {
		t.Fatalf("bare digest parsed as repo:tag: host=%q repo=%q tag=%q digest=%q",
			p.Host, p.Repo, p.Tag, p.Digest)
	}
	if p.Host != "" || p.Repo != "" {
		t.Fatalf("bare digest must have no image name: host=%q repo=%q", p.Host, p.Repo)
	}
	if p.Digest != sampleDigest {
		t.Fatalf("Digest = %q, want %q", p.Digest, sampleDigest)
	}
}

func TestParseNamedDigestPinKeepsRepo(t *testing.T) {
	raw := "nginx@" + sampleDigest
	p, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Host != DockerHubHost || p.Repo != "library/nginx" {
		t.Fatalf("host=%q repo=%q", p.Host, p.Repo)
	}
	if p.Tag != "" {
		t.Fatalf("Tag = %q, want empty", p.Tag)
	}
	if p.Digest != sampleDigest {
		t.Fatalf("Digest = %q", p.Digest)
	}
}

func TestParseRepoTagUnchanged(t *testing.T) {
	p, err := Parse("library/redis:7")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Host != DockerHubHost || p.Repo != "library/redis" || p.Tag != "7" {
		t.Fatalf("got host=%q repo=%q tag=%q", p.Host, p.Repo, p.Tag)
	}
	if p.Digest != "" {
		t.Fatalf("Digest = %q, want empty", p.Digest)
	}
}

func TestParseShortSha256PrefixStillRepoTag(t *testing.T) {
	p, err := Parse("sha256:deadbeef")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Repo != "library/sha256" || p.Tag != "deadbeef" {
		t.Fatalf("short sha256:tag must stay a Hub ref, got repo=%q tag=%q", p.Repo, p.Tag)
	}
}

func TestParseBareDigestNormalizesHex(t *testing.T) {
	upper := "SHA256:" + strings.Repeat("AB", 32)
	p, err := Parse(upper)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := "sha256:" + strings.Repeat("ab", 32)
	if p.Digest != want || p.Repo != "" || p.Tag != "" {
		t.Fatalf("got repo=%q tag=%q digest=%q", p.Repo, p.Tag, p.Digest)
	}
}
