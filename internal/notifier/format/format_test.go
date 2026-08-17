package format

import (
	"strings"
	"testing"
	"text/template"

	"github.com/BlackRaincoat/versentry/internal/model"
)

func TestChangeTextMajorSuffix(t *testing.T) {
	got := ChangeText(model.UpdateAvailable{
		CurrentTag: "2.8.1",
		LatestTag:  "3.2.0",
		Bump:       model.BumpMajor,
	}, false)
	if got != "2.8.1 → 3.2.0 (major)" {
		t.Fatalf("got %q", got)
	}
}

func TestChangeTextMinorPatchUnmarked(t *testing.T) {
	for _, tc := range []struct {
		bump string
		want string
	}{
		{model.BumpMinor, "2.8.1 → 2.9.0"},
		{model.BumpPatch, "2.8.1 → 2.8.2"},
	} {
		got := ChangeText(model.UpdateAvailable{
			CurrentTag: "2.8.1",
			LatestTag:  strings.Split(tc.want, " → ")[1],
			Bump:       tc.bump,
		}, false)
		if got != tc.want {
			t.Fatalf("bump=%s: got %q, want %q", tc.bump, got, tc.want)
		}
	}
}

func TestItemTemplateWithoutBumpStillRenders(t *testing.T) {
	tmpl, err := template.New("item").Parse(`<b>{{.Container}}</b>: {{.Change}}`)
	if err != nil {
		t.Fatal(err)
	}
	data := ItemFromEvent("prod", model.UpdateAvailable{
		Container:  model.Container{Name: "api"},
		Repo:       "example/app",
		CurrentTag: "2.8.1",
		LatestTag:  "3.2.0",
		Bump:       model.BumpMajor,
	}, true)
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "2.8.1 → 3.2.0 (major)") {
		t.Fatalf("got %q", out)
	}
}

func TestItemTemplateOptionalBumpField(t *testing.T) {
	tmpl, err := template.New("item").Parse(`{{.Change}}{{if eq .Bump "major"}}!{{end}}`)
	if err != nil {
		t.Fatal(err)
	}
	data := ItemFromEvent("", model.UpdateAvailable{
		Container:  model.Container{Name: "api"},
		CurrentTag: "2.8.1",
		LatestTag:  "3.2.0",
		Bump:       model.BumpMajor,
	}, false)
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
	if b.String() != "2.8.1 → 3.2.0 (major)!" {
		t.Fatalf("got %q", b.String())
	}
}

func TestUpdateEntryBumpOmitempty(t *testing.T) {
	entry := UpdateEntryFromEvent(model.UpdateAvailable{
		Container:  model.Container{Name: "api"},
		CurrentTag: "2.8.1",
		LatestTag:  "3.2.0",
		Bump:       model.BumpMajor,
	})
	if entry.Bump != model.BumpMajor {
		t.Fatalf("bump=%q", entry.Bump)
	}
	digestEntry := UpdateEntryFromEvent(model.UpdateAvailable{
		Container:    model.Container{Name: "web"},
		CurrentTag:   "latest",
		LocalDigest:  "sha256:aaaaaaaaaaaa",
		RemoteDigest: "sha256:bbbbbbbbbbbb",
	})
	if digestEntry.Bump != "" {
		t.Fatalf("digest bump should be empty, got %q", digestEntry.Bump)
	}
}
