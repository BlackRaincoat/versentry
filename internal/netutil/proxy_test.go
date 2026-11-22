package netutil

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/version"
)

func TestBuildHTTPClientSetsUserAgent(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("User-Agent"))
	}))
	defer srv.Close()

	client, err := BuildHTTPClient("", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	if _, err := client.Do(req); err != nil {
		t.Fatal(err)
	}
	// A caller's own User-Agent is kept, and the caller's request is not modified.
	own, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	own.Header.Set("User-Agent", "custom/1")
	if _, err := client.Do(own); err != nil {
		t.Fatal(err)
	}
	plain, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	if _, err := client.Do(plain); err != nil {
		t.Fatal(err)
	}

	want := version.UserAgent()
	if len(got) != 3 || got[0] != want || got[1] != "custom/1" || got[2] != want {
		t.Fatalf("User-Agent = %q, want [%s custom/1 %s]", got, want, want)
	}
	if plain.Header.Get("User-Agent") != "" {
		t.Fatal("caller's request was modified")
	}
	if want != "versentry/dev" {
		t.Fatalf("UserAgent() = %q for a dev build", want)
	}
}
