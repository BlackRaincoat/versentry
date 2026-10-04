package oci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/registry"
)

const (
	digestTestRepo = "library/app"
	digestTestTag  = "latest"
	indexMediaType = "application/vnd.oci.image.index.v1+json"
)

// Multi-arch index body: the digest Versentry compares against RepoDigests is
// this top-level object, for HEAD and GET alike.
var indexBody = []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[` +
	`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","size":100,"platform":{"architecture":"amd64","os":"linux"}},` +
	`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:2222222222222222222222222222222222222222222222222222222222222222","size":100,"platform":{"architecture":"arm64","os":"linux"}}]}`)

func indexDigestHex() string {
	sum := sha256.Sum256(indexBody)
	return hex.EncodeToString(sum[:])
}

type manifestCounts struct {
	head, get atomic.Int32
}

// manifestServer serves /v2/ ping and the index manifest for digestTestRepo:digestTestTag.
// onHead, when set, replaces the default HEAD answer.
func manifestServer(t *testing.T, onHead func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *manifestCounts) {
	t.Helper()
	counts := &manifestCounts{}
	manifestPath := "/v2/" + digestTestRepo + "/manifests/" + digestTestTag

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == manifestPath && r.Method == http.MethodHead:
			counts.head.Add(1)
			if onHead != nil {
				onHead(w, r)
				return
			}
			writeIndexHeaders(w, true)
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == manifestPath && r.Method == http.MethodGet:
			counts.get.Add(1)
			writeIndexHeaders(w, true)
			_, _ = w.Write(indexBody)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, counts
}

func writeIndexHeaders(w http.ResponseWriter, withDigest bool) {
	w.Header().Set("Content-Type", indexMediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(indexBody)))
	if withDigest {
		w.Header().Set("Docker-Content-Digest", "sha256:"+indexDigestHex())
	}
}

func debugLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestTagDigestUsesHeadWithoutGet(t *testing.T) {
	srv, counts := manifestServer(t, nil)
	reg := testRegistry(t, strings.TrimPrefix(srv.URL, "http://"))

	var buf bytes.Buffer
	got, err := reg.tagDigest(context.Background(), digestTestRepo, digestTestTag, debugLogger(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if got != indexDigestHex() {
		t.Fatalf("digest = %s, want index digest %s", got, indexDigestHex())
	}
	if h, g := counts.head.Load(), counts.get.Load(); h != 1 || g != 0 {
		t.Fatalf("HEAD=%d GET=%d, want 1/0 (GET counts as a Hub pull)", h, g)
	}
	if strings.Contains(buf.String(), "head fallback") {
		t.Fatalf("unexpected fallback log: %s", buf.String())
	}
}

func TestTagDigestHeadAndGetAgreeOnIndexDigest(t *testing.T) {
	// Registry without usable HEAD forces GET; the result must equal the HEAD path.
	srvHead, _ := manifestServer(t, nil)
	srvGet, _ := manifestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	})

	viaHead, err := testRegistry(t, strings.TrimPrefix(srvHead.URL, "http://")).
		tagDigest(context.Background(), digestTestRepo, digestTestTag, nil)
	if err != nil {
		t.Fatal(err)
	}
	viaGet, err := testRegistry(t, strings.TrimPrefix(srvGet.URL, "http://")).
		tagDigest(context.Background(), digestTestRepo, digestTestTag, nil)
	if err != nil {
		t.Fatal(err)
	}
	if viaHead != viaGet {
		t.Fatalf("HEAD digest %s != GET digest %s", viaHead, viaGet)
	}
}

func TestTagDigestFallsBackToGet(t *testing.T) {
	cases := []struct {
		name   string
		onHead func(w http.ResponseWriter, r *http.Request)
	}{
		{"missing Docker-Content-Digest", func(w http.ResponseWriter, r *http.Request) {
			writeIndexHeaders(w, false)
			w.WriteHeader(http.StatusOK)
		}},
		{"405 method not allowed", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}},
		{"501 not implemented", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotImplemented)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, counts := manifestServer(t, tc.onHead)
			reg := testRegistry(t, strings.TrimPrefix(srv.URL, "http://"))

			var buf bytes.Buffer
			got, err := reg.tagDigest(context.Background(), digestTestRepo, digestTestTag, debugLogger(&buf))
			if err != nil {
				t.Fatal(err)
			}
			if got != indexDigestHex() {
				t.Fatalf("digest = %s, want %s", got, indexDigestHex())
			}
			if g := counts.get.Load(); g != 1 {
				t.Fatalf("GET=%d, want 1", g)
			}
			out := buf.String()
			if !strings.Contains(out, "tag_digest head fallback") || !strings.Contains(out, "repo="+digestTestRepo) {
				t.Fatalf("missing fallback DEBUG line: %s", out)
			}
		})
	}
}

func TestTagDigestNoFallbackOnDefinitiveErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		check  func(t *testing.T, err error)
	}{
		{"404", http.StatusNotFound, func(t *testing.T, err error) {
			if !errors.Is(err, registry.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		}},
		{"403", http.StatusForbidden, func(t *testing.T, err error) {
			if !errors.Is(err, registry.ErrUnauthorized) {
				t.Fatalf("err = %v, want ErrUnauthorized", err)
			}
		}},
		{"429", http.StatusTooManyRequests, func(t *testing.T, err error) {
			var rle *registry.RateLimitError
			if !asRateLimitError(err, &rle) {
				t.Fatalf("err = %T %v, want RateLimitError", err, err)
			}
			if rle.RetryAfter != 7*time.Second {
				t.Fatalf("RetryAfter = %v, want 7s", rle.RetryAfter)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, counts := manifestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(tc.status)
			})
			reg := testRegistry(t, strings.TrimPrefix(srv.URL, "http://"))

			_, err := reg.tagDigest(context.Background(), digestTestRepo, digestTestTag, nil)
			if err == nil {
				t.Fatal("expected error")
			}
			tc.check(t, err)
			if g := counts.get.Load(); g != 0 {
				t.Fatalf("GET=%d, want 0 (no fallback on %d)", g, tc.status)
			}
		})
	}
}

func TestTagDigestNoFallbackWhenParentCtxExpires(t *testing.T) {
	srv, counts := manifestServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	reg := testRegistry(t, strings.TrimPrefix(srv.URL, "http://"))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := reg.tagDigest(ctx, digestTestRepo, digestTestTag, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if g := counts.get.Load(); g != 0 {
		t.Fatalf("GET=%d, want 0 (timeout must not double the wait)", g)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v; parent ctx should bound the call", d)
	}
}

func TestHeadNeedsGetFallbackPlainErrors(t *testing.T) {
	if headNeedsGetFallback(context.DeadlineExceeded) {
		t.Fatal("deadline must not fall back")
	}
	if headNeedsGetFallback(context.Canceled) {
		t.Fatal("cancel must not fall back")
	}
	if !headNeedsGetFallback(errors.New("HEAD https://r/v2/x/manifests/y: response did not include Docker-Content-Digest header")) {
		t.Fatal("missing header must fall back")
	}
}
