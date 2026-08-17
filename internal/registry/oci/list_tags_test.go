package oci

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// multipageTagsServer serves /v2/ ping + two Link-paginated tag pages.
// onSecondPage is called when the second tags page is requested (may block).
func multipageTagsServer(t *testing.T, onSecondPage func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	const repo = "library/postgres"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
			w.WriteHeader(http.StatusOK)
			return
		case strings.HasPrefix(r.URL.Path, "/v2/"+repo+"/tags/list"):
			if r.URL.Query().Get("last") == "" {
				next := fmt.Sprintf(`<%s/v2/%s/tags/list?n=1000&last=c>; rel="next"`, "http://"+r.Host, repo)
				w.Header().Set("Link", next)
				_, _ = w.Write([]byte(`{"name":"library/postgres","tags":["a","b","c"]}`))
				return
			}
			if onSecondPage != nil {
				onSecondPage(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"name":"library/postgres","tags":["d","e"]}`))
			return
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testRegistry(t *testing.T, host string) *Registry {
	t.Helper()
	reg, err := New(map[string]any{"host": host, "insecure": true})
	if err != nil {
		t.Fatal(err)
	}
	return reg.(*Registry)
}

func TestListTagsMultipageSuccessDebug(t *testing.T) {
	srv := multipageTagsServer(t, nil)
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := testRegistry(t, host)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	tags, err := reg.listTags(context.Background(), "library/postgres", log)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b", "c", "d", "e"}
	if len(tags) != len(want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	for i := range want {
		if tags[i] != want[i] {
			t.Fatalf("tags = %v, want %v", tags, want)
		}
	}

	out := buf.String()
	if !strings.Contains(out, "msg=list_tags") && !strings.Contains(out, "list_tags") {
		t.Fatalf("expected list_tags debug line, got %q", out)
	}
	if strings.Contains(out, "incomplete") {
		t.Fatalf("success must not log incomplete: %q", out)
	}
	if !strings.Contains(out, "pages=2") {
		t.Fatalf("want pages=2, got %q", out)
	}
	if !strings.Contains(out, "tags=5") {
		t.Fatalf("want tags=5, got %q", out)
	}
	if !strings.Contains(out, "page_max_ms=") || !strings.Contains(out, "page_avg_ms=") {
		t.Fatalf("want page_max_ms/page_avg_ms, got %q", out)
	}
}

func TestListTagsIncompleteOnCancelLogsProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	enteredSecond := make(chan struct{})
	srv := multipageTagsServer(t, func(w http.ResponseWriter, r *http.Request) {
		close(enteredSecond)
		<-r.Context().Done()
	})
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := testRegistry(t, host)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	errCh := make(chan error, 1)
	go func() {
		_, err := reg.listTags(ctx, "library/postgres", log)
		errCh <- err
	}()

	select {
	case <-enteredSecond:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for second page")
	}
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for listTags to return")
	}

	out := buf.String()
	if !strings.Contains(out, "list_tags incomplete") {
		t.Fatalf("expected incomplete debug line, got %q", out)
	}
	if !strings.Contains(out, "pages=1") {
		t.Fatalf("want pages=1 (first page completed), got %q", out)
	}
	if !strings.Contains(out, "tags=3") {
		t.Fatalf("want tags=3 from first page, got %q", out)
	}
	if !strings.Contains(out, "err=") {
		t.Fatalf("want err= attr, got %q", out)
	}
}

func TestListTagsMatchesRemoteList(t *testing.T) {
	srv := multipageTagsServer(t, nil)
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := testRegistry(t, host)

	got, err := reg.ListTags(context.Background(), "library/postgres")
	if err != nil {
		t.Fatal(err)
	}

	ref, err := name.NewRepository(host+"/library/postgres", name.WeakValidation, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	want, err := remote.List(ref, remote.WithContext(context.Background()), remote.WithAuth(reg.auth))
	if err != nil {
		t.Fatalf("remote.List: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("ListTags=%v remote.List=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListTags=%v remote.List=%v", got, want)
		}
	}
}

func TestListTagsNoDebugWorkWhenInfoLevel(t *testing.T) {
	srv := multipageTagsServer(t, nil)
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := testRegistry(t, host)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if _, err := reg.listTags(context.Background(), "library/postgres", log); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("INFO logger must stay silent, got %q", buf.String())
	}
}
