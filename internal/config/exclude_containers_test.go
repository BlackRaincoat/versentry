package config

import (
	"strings"
	"testing"
)

func TestExcludeContainerSetOK(t *testing.T) {
	set, dups, err := ExcludeContainerSet([]string{"chatwoot-notify", "watch-test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dups) != 0 {
		t.Fatalf("dups = %v", dups)
	}
	if _, ok := set["chatwoot-notify"]; !ok {
		t.Fatal("missing chatwoot-notify")
	}
	if _, ok := set["watch-test"]; !ok {
		t.Fatal("missing watch-test")
	}
}

func TestExcludeContainerSetEmptyName(t *testing.T) {
	_, _, err := ExcludeContainerSet([]string{"ok", "  "})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("got %v, want name is required", err)
	}
}

func TestExcludeContainerSetDuplicates(t *testing.T) {
	set, dups, err := ExcludeContainerSet([]string{"a", "b", "a", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 2 {
		t.Fatalf("set len = %d, want 2", len(set))
	}
	if len(dups) != 1 || dups[0] != "a" {
		t.Fatalf("dups = %v, want [a]", dups)
	}
}

func TestExcludeContainerSetTrims(t *testing.T) {
	set, _, err := ExcludeContainerSet([]string{"  umami_db  "})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set["umami_db"]; !ok {
		t.Fatalf("set = %#v", set)
	}
}

func TestExcludeContainerSetNil(t *testing.T) {
	set, dups, err := ExcludeContainerSet(nil)
	if err != nil || set != nil || dups != nil {
		t.Fatalf("set=%v dups=%v err=%v", set, dups, err)
	}
}
