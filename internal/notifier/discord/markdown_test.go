package discord

import "testing"

func TestEscapeMarkdownAllSpecials(t *testing.T) {
	raw := `\*_~|>` + "`"
	got := EscapeMarkdown(raw)
	want := `\\\*\_\~\|\>` + "\\`"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEscapeMarkdownUnderscoreInName(t *testing.T) {
	got := EscapeMarkdown("my_container_v2")
	if got != `my\_container\_v2` {
		t.Fatalf("got %q", got)
	}
}

func TestEscapeMarkdownEmpty(t *testing.T) {
	if EscapeMarkdown("") != "" {
		t.Fatal("expected empty")
	}
}
