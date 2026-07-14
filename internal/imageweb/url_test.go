package imageweb

import "testing"

func TestSemverGitHubSourceGoesToReleasesList(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://github.com/Lissy93/dashy",
	}
	got := URL("ghcr.io", "lissy93/dashy", "4.3.14", labels, ModeSemver)
	want := "https://github.com/Lissy93/dashy/releases"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSemverGitHubSourceStripsGitSuffix(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://github.com/foo/bar.git",
	}
	got := URL("index.docker.io", "foo/bar", "v1.0.0", labels, ModeSemver)
	want := "https://github.com/foo/bar/releases"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSemverNeverUsesReleasesTagPath(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://github.com/valkey-io/valkey",
	}
	got := URL("index.docker.io", "valkey/valkey", "9.1", labels, ModeSemver)
	if got != "https://github.com/valkey-io/valkey/releases" {
		t.Fatalf("got %q", got)
	}
	if contains(got, "/releases/tag/") {
		t.Fatalf("must not use /releases/tag/: %q", got)
	}
}

func TestDigestDockerHubIgnoresGitHubSource(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://github.com/valkey-io/valkey",
	}
	got := URL("index.docker.io", "valkey/valkey", "9-alpine", labels, ModeDigest)
	want := "https://hub.docker.com/r/valkey/valkey?tag=9-alpine"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDigestQuay(t *testing.T) {
	got := URL("quay.io", "prometheus/prometheus", "v2.0.0", nil, ModeDigest)
	want := "https://quay.io/repository/prometheus/prometheus?tab=tags"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDigestGHCRWithGitHubSourceUsesPkgs(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://github.com/gethomepage/homepage",
	}
	got := URL("ghcr.io", "gethomepage/homepage", "v0.9.0", labels, ModeDigest)
	want := "https://github.com/gethomepage/homepage/pkgs/container/homepage"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDigestGHCRPackageNameFromLastSegment(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://github.com/fluxcd/flux2",
	}
	got := URL("ghcr.io", "fluxcd/flux-cli", "v2.0.0", labels, ModeDigest)
	want := "https://github.com/fluxcd/flux2/pkgs/container/flux-cli"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDigestGHCRWithoutSourceIsEmpty(t *testing.T) {
	if got := URL("ghcr.io", "gethomepage/homepage", "v1", nil, ModeDigest); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestDigestNeverUsesGitReleases(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://github.com/valkey-io/valkey",
	}
	got := URL("registry.example.com", "valkey/valkey", "9-alpine", labels, ModeDigest)
	want := "https://github.com/valkey-io/valkey"
	if got != want {
		t.Fatalf("fallback source repo: got %q want %q", got, want)
	}
	if contains(got, "/releases") {
		t.Fatalf("digest must not link to releases: %q", got)
	}
}

func TestSemverDockerHubWithoutSource(t *testing.T) {
	got := URL("index.docker.io", "library/nginx", "1.25", nil, ModeSemver)
	want := "https://hub.docker.com/_/nginx?tag=1.25"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	got = URL("index.docker.io", "lissy93/dashy", "4.3.14", nil, ModeSemver)
	want = "https://hub.docker.com/r/lissy93/dashy?tag=4.3.14"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSemverGHCRWithoutSourceIsEmpty(t *testing.T) {
	if got := URL("ghcr.io", "org/app", "1.0.0", nil, ModeSemver); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestSemverGitLabSourceFallsBackToRepo(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://gitlab.com/foo/bar",
	}
	// Unknown registry for semver without github → source repo.
	got := URL("registry.gitlab.com", "foo/bar", "1.0.0", labels, ModeSemver)
	want := "https://gitlab.com/foo/bar"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSemverNonGitHubSourceOnDockerHubUsesHub(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://gitlab.com/foo/bar",
	}
	got := URL("index.docker.io", "library/nginx", "1.25", labels, ModeSemver)
	want := "https://hub.docker.com/_/nginx?tag=1.25"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUnknownRegistryWithoutSourceIsEmpty(t *testing.T) {
	if got := URL("registry.example.com", "app", "1.0.0", nil, ModeSemver); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := URL("git.example.com", "app", "1.0.0", nil, ModeDigest); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && stringIndex(s, sub) >= 0))
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
