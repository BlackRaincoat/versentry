package telegram

import (
	"strings"
	"testing"
	"text/template"

	"github.com/BlackRaincoat/versentry/internal/model"
)

func TestRenderDigestSimpleOneItem(t *testing.T) {
	n := testNotifier(t)
	text, err := n.renderDigest([]model.UpdateAvailable{{
		Container:  model.Container{Name: "caddy"},
		Host:       "index.docker.io",
		Repo:       "library/caddy",
		CurrentTag: "2.11.3",
		LatestTag:  "2.11.4",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "📦 test-host") {
		t.Fatalf("missing instance header: %q", text)
	}
	if strings.Contains(text, "updates") {
		t.Fatalf("single update should not show count: %q", text)
	}
	if !strings.Contains(text, "📦 test-host\n<b>caddy</b>") {
		t.Fatalf("expected instance on its own line above container: %q", text)
	}
}

func TestRenderDigestMultiple(t *testing.T) {
	n := testNotifier(t)
	text, err := n.renderDigest([]model.UpdateAvailable{
		{
			Container:  model.Container{Name: "caddy"},
			Repo:       "library/caddy",
			CurrentTag: "2.11.3",
			LatestTag:  "2.11.4",
		},
		{
			Container:    model.Container{Name: "diun"},
			Repo:         "crazymax/diun",
			CurrentTag:   "latest",
			LocalDigest:  "sha256:aaaaaaaaaaaa",
			RemoteDigest: "sha256:bbbbbbbbbbbb",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "— 2 updates") {
		t.Fatalf("expected count header: %q", text)
	}
	if !strings.Contains(text, "caddy") || !strings.Contains(text, "diun") {
		t.Fatalf("expected both items: %q", text)
	}
	if strings.Count(text, "📦") != 1 {
		t.Fatalf("instance header should appear once: %q", text)
	}
	// Default item ends with one \n; join is empty — compact consecutive lines.
	if !strings.Contains(text, "caddy</b>: 2.11.3 → 2.11.4\n<b>diun") {
		t.Fatalf("expected compact adjacent items, got: %q", text)
	}
	if strings.Contains(text, "2.11.4\n\n<b>diun") {
		t.Fatalf("unexpected blank line between items: %q", text)
	}
}

func TestRenderDigestBlankLineViaItemTemplate(t *testing.T) {
	// YAML |+ keeps a trailing blank line in the item template.
	itemTmpl, err := template.New("item").Parse("<b>{{.Container}}</b>: {{.Change}}\n\n")
	if err != nil {
		t.Fatal(err)
	}
	digestTmpl, err := template.New("digest").Parse(defaultDigestTemplate)
	if err != nil {
		t.Fatal(err)
	}
	n := &Notifier{
		instanceName: "test-host",
		itemTmpl:     itemTmpl,
		digestTmpl:   digestTmpl,
	}
	text, err := n.renderDigest([]model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
		{Container: model.Container{Name: "b"}, CurrentTag: "3", LatestTag: "4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "a</b>: 1 → 2\n\n<b>b") {
		t.Fatalf("expected blank line between items from template: %q", text)
	}
}

func TestInvalidTemplateFailsAtNew(t *testing.T) {
	_, err := New(map[string]any{
		"token":         "t",
		"chat_id":       "1",
		"item_template": "{{.Container",
	})
	if err == nil || !strings.Contains(err.Error(), "item_template") {
		t.Fatalf("expected item_template error, got %v", err)
	}
}

func TestUnknownModeFailsAtNew(t *testing.T) {
	_, err := New(map[string]any{
		"token":   "t",
		"chat_id": "1",
		"mode":    "batch",
	})
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("expected mode error, got %v", err)
	}
}

func testNotifier(t *testing.T) *Notifier {
	t.Helper()
	itemTmpl, err := template.New("item").Parse(defaultItemTemplate)
	if err != nil {
		t.Fatal(err)
	}
	digestTmpl, err := template.New("digest").Parse(defaultDigestTemplate)
	if err != nil {
		t.Fatal(err)
	}
	return &Notifier{
		instanceName: "test-host",
		itemTmpl:     itemTmpl,
		digestTmpl:   digestTmpl,
	}
}
