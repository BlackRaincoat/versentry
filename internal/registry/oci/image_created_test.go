package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

type blob struct {
	body      []byte
	mediaType string
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// imageFixture returns config + manifest blobs for an image built at created.
func imageFixture(created string) (cfg, manifest blob) {
	cfg = blob{
		body:      []byte(fmt.Sprintf(`{"architecture":"amd64","os":"linux","created":%q,"rootfs":{"type":"layers","diff_ids":[]},"config":{}}`, created)),
		mediaType: "application/vnd.oci.image.config.v1+json",
	}
	manifest = blob{
		body: []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},"layers":[]}`,
			sha(cfg.body), len(cfg.body))),
		mediaType: "application/vnd.oci.image.manifest.v1+json",
	}
	return cfg, manifest
}

// registryFixture serves manifests (by tag or digest) and blobs (by digest) for repo.
func registryFixture(t *testing.T, repo string, manifests map[string]blob, blobs map[string]blob) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			return
		}
		serve := func(b blob, ok bool) {
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", b.mediaType)
			w.Header().Set("Content-Length", strconv.Itoa(len(b.body)))
			w.Header().Set("Docker-Content-Digest", sha(b.body))
			_, _ = w.Write(b.body)
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/v2/"+repo+"/manifests/"):
			b, ok := manifests[strings.TrimPrefix(r.URL.Path, "/v2/"+repo+"/manifests/")]
			serve(b, ok)
		case strings.HasPrefix(r.URL.Path, "/v2/"+repo+"/blobs/"):
			b, ok := blobs[strings.TrimPrefix(r.URL.Path, "/v2/"+repo+"/blobs/")]
			serve(b, ok)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImageCreatedSingleManifest(t *testing.T) {
	cfg, man := imageFixture("2026-09-20T10:00:00Z")
	srv := registryFixture(t, "org/app",
		map[string]blob{"1.2.0": man},
		map[string]blob{sha(cfg.body): cfg})

	got, err := testRegistry(t, strings.TrimPrefix(srv.URL, "http://")).ImageCreated(context.Background(), "org/app", "1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("created = %v, want %v", got, want)
	}
}

func TestImageCreatedIndexPicksOwnPlatformNotAttestation(t *testing.T) {
	otherArch := "s390x"
	if runtime.GOARCH == otherArch {
		otherArch = "ppc64le"
	}
	cfgOwn, manOwn := imageFixture("2026-09-21T00:00:00Z")
	cfgOther, manOther := imageFixture("2020-01-01T00:00:00Z")
	cfgAtt, manAtt := imageFixture("2019-01-01T00:00:00Z")

	child := func(m blob, os, arch string) string {
		return fmt.Sprintf(`{"mediaType":%q,"digest":%q,"size":%d,"platform":{"os":%q,"architecture":%q}}`,
			m.mediaType, sha(m.body), len(m.body), os, arch)
	}
	// Order: attestation, other arch, own arch — own must win.
	index := blob{
		body: []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[` +
			child(manAtt, "unknown", "unknown") + "," +
			child(manOther, "linux", otherArch) + "," +
			child(manOwn, runtime.GOOS, runtime.GOARCH) + `]}`),
		mediaType: "application/vnd.oci.image.index.v1+json",
	}
	srv := registryFixture(t, "org/app",
		map[string]blob{
			"latest": index, sha(manOwn.body): manOwn, sha(manOther.body): manOther, sha(manAtt.body): manAtt,
		},
		map[string]blob{sha(cfgOwn.body): cfgOwn, sha(cfgOther.body): cfgOther, sha(cfgAtt.body): cfgAtt})

	got, err := testRegistry(t, strings.TrimPrefix(srv.URL, "http://")).ImageCreated(context.Background(), "org/app", "latest")
	if err != nil {
		t.Fatal(err)
	}
	if got.Year() != 2026 {
		t.Fatalf("created = %v, want own-platform image (2026)", got)
	}
}

func TestPickPlatformChildFallsBackToFirstRealPlatform(t *testing.T) {
	img := types.OCIManifestSchema1
	children := []v1.Descriptor{
		{MediaType: img, Digest: v1.Hash{Algorithm: "sha256", Hex: "a"}, Platform: &v1.Platform{OS: "unknown", Architecture: "unknown"}},
		{MediaType: img, Digest: v1.Hash{Algorithm: "sha256", Hex: "b"}, Platform: &v1.Platform{OS: "linux", Architecture: "riscv64"}},
		{MediaType: img, Digest: v1.Hash{Algorithm: "sha256", Hex: "c"}, Platform: &v1.Platform{OS: "linux", Architecture: "mips"}},
	}
	got, ok := pickPlatformChild(children, "linux", "amd64")
	if !ok || got.Digest.Hex != "b" {
		t.Fatalf("got %v ok=%v, want first real platform (b)", got.Digest, ok)
	}
	if _, ok := pickPlatformChild(children[:1], "linux", "amd64"); ok {
		t.Fatal("attestation-only index must not yield a child")
	}
}

func TestImageCreatedNotFound(t *testing.T) {
	srv := registryFixture(t, "org/app", map[string]blob{}, map[string]blob{})
	_, err := testRegistry(t, strings.TrimPrefix(srv.URL, "http://")).ImageCreated(context.Background(), "org/app", "nope")
	if err == nil {
		t.Fatal("expected error")
	}
}
