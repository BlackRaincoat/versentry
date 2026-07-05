package imageweb

import "testing"

func TestGitHubSourceReleaseURL(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://github.com/Lissy93/dashy",
	}
	got := URL("ghcr.io", "lissy93/dashy", "4.3.14", labels)
	want := "https://github.com/Lissy93/dashy/releases/tag/4.3.14"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestGitHubSourceStripsGitSuffix(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://github.com/foo/bar.git",
	}
	got := URL("index.docker.io", "foo/bar", "v1.0.0", labels)
	want := "https://github.com/foo/bar/releases/tag/v1.0.0"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDockerHubOfficialAndUser(t *testing.T) {
	got := URL("index.docker.io", "library/nginx", "1.25", nil)
	want := "https://hub.docker.com/_/nginx?tag=1.25"
	if got != want {
		t.Fatalf("official: got %q want %q", got, want)
	}

	got = URL("index.docker.io", "lissy93/dashy", "4.3.14", nil)
	want = "https://hub.docker.com/r/lissy93/dashy?tag=4.3.14"
	if got != want {
		t.Fatalf("user: got %q want %q", got, want)
	}
}

func TestUnknownRegistryWithoutSourceIsEmpty(t *testing.T) {
	if got := URL("ghcr.io", "org/app", "1.0.0", nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := URL("registry.gitlab.com", "group/app", "1.0.0", nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := URL("git.example.com", "app", "1.0.0", nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestNonGitHubSourceFallsBackToRegistry(t *testing.T) {
	labels := map[string]string{
		"org.opencontainers.image.source": "https://gitlab.com/foo/bar",
	}
	got := URL("index.docker.io", "library/nginx", "1.25", labels)
	want := "https://hub.docker.com/_/nginx?tag=1.25"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
