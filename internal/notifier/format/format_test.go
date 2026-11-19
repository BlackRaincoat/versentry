package format

import (
	"encoding/json"
	"strings"
	"testing"
	"text/template"
	"time"

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

func TestChangeTextReminderSuffix(t *testing.T) {
	semverReminder := ChangeText(model.UpdateAvailable{
		CurrentTag: "2.8.1", LatestTag: "3.2.0", Bump: model.BumpMajor, Reminder: true,
	}, false)
	if semverReminder != "2.8.1 → 3.2.0 (major) (reminder)" {
		t.Fatalf("got %q", semverReminder)
	}
	digestReminder := ChangeText(model.UpdateAvailable{
		CurrentTag: "latest", LocalDigest: "sha256:aaaaaaaaaaaaaaaa", RemoteDigest: "sha256:bbbbbbbbbbbbbbbb", Reminder: true,
	}, false)
	if !strings.HasSuffix(digestReminder, " (reminder)") || !strings.HasPrefix(digestReminder, "digest changed: ") {
		t.Fatalf("got %q", digestReminder)
	}
}

func TestReminderFieldInTemplateAndJSON(t *testing.T) {
	event := model.UpdateAvailable{CurrentTag: "1", LatestTag: "2", Reminder: true}
	if !ItemFromEvent("prod", event, false).Reminder {
		t.Fatal("ItemData.Reminder not set")
	}
	if !UpdateEntryFromEvent(event).Reminder {
		t.Fatal("UpdateEntry.Reminder not set")
	}
	event.Reminder = false
	if UpdateEntryFromEvent(event).Reminder {
		t.Fatal("reminder must default to false")
	}
}

func TestChangeTextDigestVersions(t *testing.T) {
	checked := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	base := model.UpdateAvailable{
		CurrentTag: "latest", LocalDigest: "sha256:aaaaaaaaaaaaaaaa", RemoteDigest: "sha256:bbbbbbbbbbbbbbbb",
		CheckedAt: checked,
	}
	cases := []struct {
		name    string
		cur     string
		latest  string
		created time.Time
		want    string
	}{
		{"version change", "2.3.1", "2.4.0", checked.Add(-72 * time.Hour), "2.3.1 → 2.4.0 (built 3 days ago)"},
		{"rebuild same version", "2.3.1", "2.3.1", checked.Add(-5 * time.Hour), "2.3.1 rebuilt (built 5 hours ago)"},
		{"running image unlabeled", "", "2.4.0", time.Time{}, "new image 2.4.0"},
		{"no version, build time", "", "", checked.Add(-90 * time.Minute), "digest changed: aaaaaaaaaaaa… → bbbbbbbbbbbb… (built 1 hour ago)"},
		{"nothing known", "2.3.1", "", time.Time{}, "digest changed: aaaaaaaaaaaa… → bbbbbbbbbbbb…"},
		{"fresh", "1", "2", checked.Add(-10 * time.Minute), "1 → 2 (built less than an hour ago)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := base
			e.CurrentVersion, e.LatestVersion, e.LatestCreated = tc.cur, tc.latest, tc.created
			if got := ChangeText(e, false); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChangeTextBuiltBeforeReminder(t *testing.T) {
	e := model.UpdateAvailable{
		CurrentTag: "1.0.0", LatestTag: "2.0.0", Bump: model.BumpMajor, Reminder: true,
		CheckedAt: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), LatestCreated: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}
	if got := ChangeText(e, false); got != "1.0.0 → 2.0.0 (major) (built 9 days ago) (reminder)" {
		t.Fatalf("got %q", got)
	}
}

func TestVersionFieldsInTemplateAndJSON(t *testing.T) {
	created := time.Date(2026, 9, 28, 8, 30, 0, 0, time.FixedZone("MSK", 3*3600))
	e := model.UpdateAvailable{
		Container: model.Container{Name: "app"}, Repo: "org/app", CurrentTag: "latest",
		RemoteDigest: "sha256:bb", CheckedAt: created.Add(48 * time.Hour),
		CurrentVersion: "2.3.1", LatestVersion: "2.4.0", LatestCreated: created,
	}
	item := ItemFromEvent("prod", e, true)
	if item.CurrentVersion != "2.3.1" || item.LatestVersion != "2.4.0" || item.Built != "2 days ago" {
		t.Fatalf("item = %+v", item)
	}
	raw, err := json.Marshal(UpdateEntryFromEvent(e))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"current_version":"2.3.1"`, `"latest_version":"2.4.0"`, `"latest_created":"2026-09-28T05:30:00Z"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON %s missing %s", raw, want)
		}
	}
	plain, _ := json.Marshal(UpdateEntryFromEvent(model.UpdateAvailable{CurrentTag: "latest"}))
	if strings.Contains(string(plain), "version") || strings.Contains(string(plain), "latest_created") {
		t.Fatalf("unknown details must be omitted: %s", plain)
	}
}
