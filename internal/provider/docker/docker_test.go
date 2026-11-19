package docker

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/model"
)

// fakeDaemon is a minimal Docker Engine API served on a unix socket, so the
// provider is exercised through the real moby client (version negotiation,
// paths, JSON decoding) without a daemon.
type fakeDaemon struct {
	t          *testing.T
	apiVersion string // API-Version header on /_ping; "" = omitted
	pingStatus int    // 0 = 200

	containers     []map[string]any          // GET /containers/json
	containerByID  map[string]map[string]any // GET /containers/{id}/json
	images         map[string]map[string]any // GET /images/{id}/json
	listStatus     int                       // non-zero: /containers/json fails with it
	imageInspectOK map[string]bool           // nil = every known image answers

	mu       sync.Mutex
	requests []string
}

var versionPrefix = regexp.MustCompile(`^/v[0-9.]+`)

func (d *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	d.requests = append(d.requests, r.Method+" "+r.URL.RequestURI())
	d.mu.Unlock()

	p := versionPrefix.ReplaceAllString(r.URL.Path, "")
	switch {
	case p == "/_ping":
		if d.apiVersion != "" {
			w.Header().Set("API-Version", d.apiVersion)
		}
		w.Header().Set("OSType", "linux")
		status := d.pingStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte("OK"))
		}
	case p == "/containers/json":
		if d.listStatus != 0 {
			writeError(w, d.listStatus, "list failed")
			return
		}
		writeJSON(w, d.containers)
	case strings.HasPrefix(p, "/containers/") && strings.HasSuffix(p, "/json"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/containers/"), "/json")
		if c, ok := d.containerByID[id]; ok {
			writeJSON(w, c)
			return
		}
		writeError(w, http.StatusNotFound, "No such container: "+id)
	case strings.HasPrefix(p, "/images/") && strings.HasSuffix(p, "/json"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/images/"), "/json")
		img, ok := d.images[id]
		if ok && d.imageInspectOK != nil && !d.imageInspectOK[id] {
			ok = false
		}
		if ok {
			writeJSON(w, img)
			return
		}
		writeError(w, http.StatusNotFound, "No such image: "+id)
	default:
		d.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		writeError(w, http.StatusNotFound, "unexpected")
	}
}

func (d *fakeDaemon) count(substr string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, r := range d.requests {
		if strings.Contains(r, substr) {
			n++
		}
	}
	return n
}

func (d *fakeDaemon) requestsMatching(substr string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, r := range d.requests {
		if strings.Contains(r, substr) {
			out = append(out, r)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// socketDir returns a short directory: unix socket paths are limited to ~104 bytes.
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "vsd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// newTestProvider starts d on a unix socket and builds the provider through
// New with the socket config key, isolated from the host's DOCKER_* env.
func newTestProvider(t *testing.T, d *fakeDaemon) *Provider {
	t.Helper()
	d.t = t
	if d.apiVersion == "" {
		d.apiVersion = "1.47"
	}
	for _, k := range []string{"DOCKER_HOST", "DOCKER_API_VERSION", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY"} {
		t.Setenv(k, "")
	}

	sock := filepath.Join(socketDir(t), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("unix socket not available: %v", err)
	}
	srv := httptest.NewUnstartedServer(d)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	p, err := New(map[string]any{"socket": sock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p.(*Provider)
}

func TestPing(t *testing.T) {
	d := &fakeDaemon{}
	p := newTestProvider(t, d)
	if err := p.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if d.count("/_ping") == 0 {
		t.Fatal("Ping did not reach /_ping")
	}
}

func TestPingSocketMissing(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	sock := filepath.Join(socketDir(t), "absent.sock")
	p, err := New(map[string]any{"socket": sock})
	if err != nil {
		t.Fatalf("New must not dial: %v", err)
	}
	err = p.(*Provider).Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "docker ping") {
		t.Fatalf("Ping on missing socket = %v, want docker ping error", err)
	}
}

func TestPingReportsErrorStatus(t *testing.T) {
	// Despite the moby doc comment ("ignores internal server errors"), client
	// v0.5.0 returns the GET /_ping error for any non-200 — an unhealthy daemon
	// or a socket proxy denying /_ping makes health.Check fail.
	for _, status := range []int{http.StatusInternalServerError, http.StatusForbidden} {
		d := &fakeDaemon{pingStatus: status}
		p := newTestProvider(t, d)
		if err := p.Ping(context.Background()); err == nil {
			t.Errorf("Ping with %d = nil, want error", status)
		}
	}
}

func TestListRunning(t *testing.T) {
	d := &fakeDaemon{
		containers: []map[string]any{
			{
				"Id": "c1", "Names": []string{"/web"}, "Image": "nginx:1.27",
				"ImageID": "sha256:img1",
				"Labels": map[string]string{
					"versentry.include": "^1\\.27\\.",
					"com.example.team":  "ops",
				},
			},
			{
				"Id": "c2", "Names": []string{"/db"}, "Image": "postgres:16",
				"ImageID": "sha256:img2",
			},
			{
				// Same image as c1: inspected once per pass.
				"Id": "c3", "Names": []string{"/web-2", "/alias"}, "Image": "nginx:1.27",
				"ImageID": "sha256:img1",
			},
			{"Id": "c4", "Names": []string{}, "Image": "sha256:abc", "ImageID": ""},
		},
		images: map[string]map[string]any{
			"sha256:img1": {
				"Id": "sha256:img1",
				"Config": map[string]any{"Labels": map[string]string{
					"org.opencontainers.image.source": "https://github.com/nginx/docker-nginx",
					"com.example.team":                "image-default",
				}},
			},
			"sha256:img2": {"Id": "sha256:img2", "Config": map[string]any{}},
		},
	}
	p := newTestProvider(t, d)

	got, err := p.ListRunning(context.Background())
	if err != nil {
		t.Fatalf("ListRunning: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d containers, want 4: %+v", len(got), got)
	}

	web := got[0]
	if web.ID != "c1" || web.Name != "web" || web.ImageRef != "nginx:1.27" {
		t.Fatalf("web = %+v", web)
	}
	want := map[string]string{
		"versentry.include":               "^1\\.27\\.",
		"com.example.team":                "ops", // container wins
		"org.opencontainers.image.source": "https://github.com/nginx/docker-nginx",
	}
	for k, v := range want {
		if web.Labels[k] != v {
			t.Errorf("web label %s = %q, want %q", k, web.Labels[k], v)
		}
	}

	if got[1].Name != "db" || len(got[1].Labels) != 0 {
		t.Errorf("db = %+v, want no labels", got[1])
	}
	if got[2].Name != "web-2" {
		t.Errorf("first name wins, got %q", got[2].Name)
	}
	if got[2].Labels["org.opencontainers.image.source"] == "" {
		t.Error("cached image labels missing on second container")
	}
	if got[3].Name != "" || got[3].ImageRef != "sha256:abc" {
		t.Errorf("nameless container = %+v", got[3])
	}

	if n := d.count("/images/sha256:img1/json"); n != 1 {
		t.Errorf("image img1 inspected %d times, want 1 (per-pass cache)", n)
	}
	if n := d.count("/images//json"); n != 0 {
		t.Error("empty ImageID must not be inspected")
	}
	for _, r := range d.requestsMatching("/containers/json") {
		if strings.Contains(r, "all=") {
			t.Errorf("list must ask for running containers only: %s", r)
		}
	}
}

func TestListRunningDropsImageBakedControlLabels(t *testing.T) {
	// Docker copies image labels onto the container: a versentry.* value equal
	// to the image's is image-provided and must not override central config.
	d := &fakeDaemon{
		containers: []map[string]any{{
			"Id": "c1", "Names": []string{"/app"}, "Image": "vendor/app:1.0",
			"ImageID": "sha256:img",
			"Labels": map[string]string{
				"versentry.watch":   "false", // copied from image
				"versentry.track":   "auto",  // set in compose (differs from image)
				"versentry.include": "^1\\.", // set in compose, image has none
			},
		}},
		images: map[string]map[string]any{
			"sha256:img": {"Id": "sha256:img", "Config": map[string]any{"Labels": map[string]string{
				"versentry.watch": "false",
				"versentry.track": "digest",
			}}},
		},
	}
	p := newTestProvider(t, d)
	got, err := p.ListRunning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	l := got[0].Labels
	if _, ok := l["versentry.watch"]; ok {
		t.Error("image-baked versentry.watch must be dropped")
	}
	if l["versentry.track"] != "auto" || l["versentry.include"] != "^1\\." {
		t.Errorf("compose-set control labels lost: %v", l)
	}
}

func TestListRunningImageInspectFailureKeepsContainerLabels(t *testing.T) {
	// Image inspect failing (image removed mid-pass) must not drop the container
	// or its labels; without image labels nothing is filtered as image-baked.
	d := &fakeDaemon{
		containers: []map[string]any{{
			"Id": "c1", "Names": []string{"/app"}, "Image": "app:1",
			"ImageID": "sha256:gone",
			"Labels":  map[string]string{"versentry.watch": "false"},
		}},
	}
	p := newTestProvider(t, d)
	got, err := p.ListRunning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Labels["versentry.watch"] != "false" {
		t.Fatalf("got %+v", got)
	}
}

func TestListRunningAPIError(t *testing.T) {
	d := &fakeDaemon{listStatus: http.StatusInternalServerError}
	p := newTestProvider(t, d)
	_, err := p.ListRunning(context.Background())
	if err == nil || !strings.Contains(err.Error(), "list containers") {
		t.Fatalf("err = %v, want list containers error", err)
	}
}

func TestListRunningNegotiatesOldAPIVersion(t *testing.T) {
	d := &fakeDaemon{apiVersion: "1.41", containers: []map[string]any{}}
	p := newTestProvider(t, d)
	if _, err := p.ListRunning(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(d.requestsMatching("/v1.41/containers/json")) != 1 {
		t.Fatalf("want request downgraded to the daemon's API: %v", d.requestsMatching("/containers/json"))
	}
}

func TestListRunningRejectsTooOldDaemon(t *testing.T) {
	// Below the client's MinAPIVersion (1.40): negotiation fails silently and
	// the client keeps its own maximum version; a real old daemon answers 400
	// "client version is too new", which must surface as an error.
	d := &fakeDaemon{apiVersion: "1.24", listStatus: http.StatusBadRequest}
	p := newTestProvider(t, d)
	if _, err := p.ListRunning(context.Background()); err == nil {
		t.Fatal("want error against a daemon below the client's minimum API")
	}
	reqs := d.requestsMatching("/containers/json")
	if len(reqs) != 1 || strings.Contains(reqs[0], "/v1.24/") {
		t.Fatalf("requests = %v, want one request not downgraded to 1.24", reqs)
	}
}

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func digestDaemon(repoDigests []string) *fakeDaemon {
	return &fakeDaemon{
		containerByID: map[string]map[string]any{
			"c1": {"Id": "c1", "Name": "/web", "Image": "sha256:img"},
		},
		images: map[string]map[string]any{
			"sha256:img": {"Id": "sha256:img", "RepoDigests": repoDigests},
		},
	}
}

func TestLocalDigest(t *testing.T) {
	c := model.Container{ID: "c1", Name: "web"}
	const hub, ghcr = "index.docker.io", "ghcr.io"
	cases := []struct {
		name        string
		repoDigests []string
		host, repo  string
		want        string
		wantErr     bool
	}{
		{"docker hub short form", []string{"nginx@" + digestA}, hub, "library/nginx", digestA, false},
		{"docker.io spelling", []string{"docker.io/library/nginx@" + digestA}, hub, "library/nginx", digestA, false},
		{"pick matching repo", []string{"ghcr.io/acme/tool@" + digestB, "acme/app@" + digestA}, hub, "acme/app", digestA, false},
		// Same image pulled from two registries: one entry per registry, digests may differ.
		{"pick matching registry", []string{"ghcr.io/acme/app@" + digestB, "acme/app@" + digestA}, hub, "acme/app", digestA, false},
		{"pick matching registry ghcr", []string{"acme/app@" + digestA, "ghcr.io/acme/app@" + digestB}, ghcr, "acme/app", digestB, false},
		{"same path on another registry only", []string{"ghcr.io/acme/app@" + digestB}, hub, "acme/app", "", true},
		{"registry with port", []string{"localhost:5000/acme/app@" + digestA}, "localhost:5000", "acme/app", digestA, false},
		{"no match", []string{"other/app@" + digestA}, hub, "acme/app", "", true},
		{"locally built", nil, hub, "acme/app", "", true},
		{"unparseable entry skipped", []string{"not a ref@@", "acme/app@" + digestA}, hub, "acme/app", digestA, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestProvider(t, digestDaemon(tc.repoDigests))
			got, err := p.LocalDigest(context.Background(), c, tc.host, tc.repo)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %q, want error", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestLocalDigestUsesContainerImageNotTag(t *testing.T) {
	// The container's image ID is inspected, not its tag: after `docker pull`
	// the tag points at the new image while the container still runs the old one.
	d := digestDaemon([]string{"acme/app@" + digestA})
	p := newTestProvider(t, d)
	if _, err := p.LocalDigest(context.Background(), model.Container{ID: "c1", ImageRef: "acme/app:latest"}, "index.docker.io", "acme/app"); err != nil {
		t.Fatal(err)
	}
	if d.count("/images/sha256:img/json") != 1 || d.count("/images/acme") != 0 {
		t.Fatalf("requests = %v", d.requestsMatching("/images/"))
	}
}

func TestLocalDigestErrors(t *testing.T) {
	d := digestDaemon([]string{"acme/app@" + digestA})
	p := newTestProvider(t, d)
	_, err := p.LocalDigest(context.Background(), model.Container{ID: "missing", Name: "x"}, "index.docker.io", "acme/app")
	if err == nil || !strings.Contains(err.Error(), "inspect container") {
		t.Fatalf("missing container: %v", err)
	}

	d = digestDaemon(nil)
	d.imageInspectOK = map[string]bool{}
	p = newTestProvider(t, d)
	_, err = p.LocalDigest(context.Background(), model.Container{ID: "c1", Name: "web"}, "index.docker.io", "acme/app")
	if err == nil || !strings.Contains(err.Error(), "inspect image") {
		t.Fatalf("image inspect failure: %v", err)
	}
}
