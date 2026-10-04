package docker

import "testing"

func TestMergeLabelsDropsImageBakedControlLabels(t *testing.T) {
	img := map[string]string{
		"org.opencontainers.image.source": "https://github.com/example/app",
		"versentry.include":               `^.*$`, // baked in by the image publisher
		"versentry.watch":                 "true",
		"versentry.track":                 "digest",
	}
	ctr := map[string]string{
		// Docker copies image labels into the container:
		"org.opencontainers.image.source": "https://github.com/example/app",
		"versentry.include":               `^.*$`,
		"versentry.watch":                 "true",
		// …and the user overrode one in compose:
		"versentry.track":            "auto",
		"versentry.min_age":          "7d", // user-only key
		"com.docker.compose.service": "app",
	}
	got := mergeLabels(img, ctr)

	for _, k := range []string{"versentry.include", "versentry.watch"} {
		if _, ok := got[k]; ok {
			t.Fatalf("%s from the image must be dropped, got %q", k, got[k])
		}
	}
	if got["versentry.track"] != "auto" || got["versentry.min_age"] != "7d" {
		t.Fatalf("user control labels must survive: %v", got)
	}
	if got["org.opencontainers.image.source"] == "" || got["com.docker.compose.service"] != "app" {
		t.Fatalf("non-control labels must be kept: %v", got)
	}
}

func TestMergeLabelsWithoutImageLabelsKeepsEverything(t *testing.T) {
	got := mergeLabels(nil, map[string]string{"versentry.watch": "false"})
	if got["versentry.watch"] != "false" {
		t.Fatalf("got %v", got)
	}
}

func TestMergeLabelsImageOnlyControlLabelIgnored(t *testing.T) {
	// Image label not copied into container labels (other engines / API paths).
	got := mergeLabels(map[string]string{"versentry.watch": "true", "a": "b"}, map[string]string{})
	if _, ok := got["versentry.watch"]; ok || got["a"] != "b" {
		t.Fatalf("got %v", got)
	}
}
