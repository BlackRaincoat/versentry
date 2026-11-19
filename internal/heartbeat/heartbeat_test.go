package heartbeat

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
)

type hit struct {
	method, path, contentType, body string
}

func server(t *testing.T, status int) (*httptest.Server, func() []hit) {
	t.Helper()
	var mu sync.Mutex
	var hits []hit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, hit{r.Method, r.URL.Path, r.Header.Get("Content-Type"), string(b)})
		mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []hit { mu.Lock(); defer mu.Unlock(); return append([]hit(nil), hits...) }
}

func newPinger(t *testing.T, cfg config.HeartbeatConfig) (*Pinger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	p, err := New(cfg, slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		p.sleep = func(context.Context, time.Duration) error { return nil }
	}
	return p, &buf
}

func TestNoURLIsOff(t *testing.T) {
	p, _ := newPinger(t, config.HeartbeatConfig{})
	if p != nil {
		t.Fatal("empty url must disable heartbeat")
	}
	p.Report(context.Background(), true, "x") // nil receiver: no panic
}

func TestSuccessPostsSummaryToURL(t *testing.T) {
	srv, hits := server(t, http.StatusOK)
	p, logs := newPinger(t, config.HeartbeatConfig{URL: srv.URL + "/ping/uuid-1", FailURL: srv.URL + "/ping/uuid-1/fail"})
	p.Report(context.Background(), true, "versentry: checked=3")
	got := hits()
	if len(got) != 1 || got[0].method != http.MethodPost || got[0].path != "/ping/uuid-1" ||
		got[0].body != "versentry: checked=3" || !strings.HasPrefix(got[0].contentType, "text/plain") {
		t.Fatalf("hits = %+v", got)
	}
	if !strings.Contains(logs.String(), "heartbeat sent") || !strings.Contains(logs.String(), "status=ok") {
		t.Fatalf("log = %s", logs.String())
	}
}

func TestFailureUsesFailURL(t *testing.T) {
	srv, hits := server(t, http.StatusOK)
	p, _ := newPinger(t, config.HeartbeatConfig{URL: srv.URL + "/ping/u", FailURL: srv.URL + "/ping/u/fail"})
	p.Report(context.Background(), false, "versentry: check failed: list containers")
	if got := hits(); len(got) != 1 || got[0].path != "/ping/u/fail" {
		t.Fatalf("hits = %+v", got)
	}
}

func TestFailureWithoutFailURLSendsNothing(t *testing.T) {
	srv, hits := server(t, http.StatusOK)
	p, logs := newPinger(t, config.HeartbeatConfig{URL: srv.URL + "/ping/u"})
	p.Report(context.Background(), false, "x")
	if len(hits()) != 0 {
		t.Fatal("no fail_url: the missed success ping must do the alerting")
	}
	if !strings.Contains(logs.String(), "missed ping will alert") {
		t.Fatalf("log = %s", logs.String())
	}
}

func TestServerErrorRetriedThenWarns(t *testing.T) {
	srv, hits := server(t, http.StatusInternalServerError)
	p, logs := newPinger(t, config.HeartbeatConfig{URL: srv.URL + "/ping/secret-uuid"})
	p.Report(context.Background(), true, "x")
	if n := len(hits()); n != maxAttempts {
		t.Fatalf("attempts = %d, want %d", n, maxAttempts)
	}
	out := logs.String()
	if !strings.Contains(out, "heartbeat ping failed") || strings.Contains(out, "secret-uuid") {
		t.Fatalf("want WARN without the secret path: %s", out)
	}
}

func TestNetworkErrorDoesNotLeakURL(t *testing.T) {
	p, logs := newPinger(t, config.HeartbeatConfig{URL: "http://127.0.0.1:9/ping/secret-uuid"})
	p.Report(context.Background(), true, "x")
	if out := logs.String(); strings.Contains(out, "secret-uuid") || !strings.Contains(out, "heartbeat ping failed") {
		t.Fatalf("log = %s", out)
	}
}

func TestCanceledContextSkips(t *testing.T) {
	srv, hits := server(t, http.StatusOK)
	p, _ := newPinger(t, config.HeartbeatConfig{URL: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Report(ctx, false, "x")
	if len(hits()) != 0 {
		t.Fatal("shutdown is not a failed check")
	}
}

func TestTimeoutRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			select { // first attempt outlives the client timeout
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	p, logs := newPinger(t, config.HeartbeatConfig{
		URL:     srv.URL + "/ping/u",
		Timeout: config.Duration{Duration: 100 * time.Millisecond},
	})
	p.Report(context.Background(), true, "x")
	if n := calls.Load(); n != 2 {
		t.Fatalf("attempts = %d, want 2 (timeout must be retried)", n)
	}
	if !strings.Contains(logs.String(), "heartbeat sent") {
		t.Fatalf("log = %s", logs.String())
	}
}
