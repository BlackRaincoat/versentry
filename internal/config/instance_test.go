package config

import "testing"

func TestResolveInstanceNameConfigured(t *testing.T) {
	if got := ResolveInstanceName("myserver"); got != "myserver" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveInstanceNameSkipsContainerID(t *testing.T) {
	if !looksLikeContainerID("8145fc52b5fd") {
		t.Fatal("sanity check")
	}
	// When kernel hostname is a container id, fall back to default.
	got := ResolveInstanceName("")
	if got == "8145fc52b5fd" {
		t.Fatal("should not use container id")
	}
}

func TestLooksLikeContainerID(t *testing.T) {
	if !looksLikeContainerID("8145fc52b5fd") {
		t.Fatal("expected container id match")
	}
	if looksLikeContainerID("myserver") {
		t.Fatal("hostname should not match")
	}
	if looksLikeContainerID("8145fc52b5fda") {
		t.Fatal("13 chars should not match")
	}
}