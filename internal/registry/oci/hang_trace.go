package oci

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"
)

type httpTripSinkKey struct{}
type httpTripLogKey struct{}

// httpTripSink records per-ListTags/TagDigest HTTP phases so incomplete logs
// can show where time went (connect / TLS / waiting_headers) and how many
// RoundTrips ran (library-internal retries vs one long wait).
type httpTripSink struct {
	mu    sync.Mutex
	trips int
	last  httpTripSnapshot
}

type httpTripSnapshot struct {
	host     string
	path     string
	phase    string
	duration time.Duration
	reused   bool
	wasIdle  bool
	idle     time.Duration
	remote   string
	err      string
}

func withHTTPTripSink(ctx context.Context, sink *httpTripSink, log *slog.Logger) context.Context {
	ctx = context.WithValue(ctx, httpTripSinkKey{}, sink)
	if log != nil {
		ctx = context.WithValue(ctx, httpTripLogKey{}, log)
	}
	return ctx
}

func httpTripSinkFrom(ctx context.Context) *httpTripSink {
	sink, _ := ctx.Value(httpTripSinkKey{}).(*httpTripSink)
	return sink
}

func httpTripLogFrom(ctx context.Context) *slog.Logger {
	log, _ := ctx.Value(httpTripLogKey{}).(*slog.Logger)
	return log
}

func (s *httpTripSink) record(snap httpTripSnapshot) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.trips++
	s.last = snap
	s.mu.Unlock()
}

func (s *httpTripSink) slogAttrs() []any {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.trips == 0 {
		return nil
	}
	attrs := []any{
		"http_trips", s.trips,
		"last_phase", s.last.phase,
		"last_ms", s.last.duration.Milliseconds(),
		"last_path", s.last.path,
		"reused", s.last.reused,
	}
	if s.last.wasIdle {
		attrs = append(attrs, "idle_ms", s.last.idle.Milliseconds())
	}
	if s.last.remote != "" {
		attrs = append(attrs, "remote", s.last.remote)
	}
	return attrs
}

type hangTraceTransport struct {
	base http.RoundTripper
}

func withHangTrace(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		return nil
	}
	return &hangTraceTransport{base: base}
}

func (t *hangTraceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	log := httpTripLogFrom(req.Context())
	if log == nil {
		log = slog.Default()
	}
	debug := log != nil && log.Enabled(req.Context(), slog.LevelDebug)
	sink := httpTripSinkFrom(req.Context())

	rec := &httpTripRecorder{
		start: time.Now(),
		host:  req.URL.Host,
		path:  req.URL.EscapedPath(),
		phase: "dial",
	}
	ctx := httptrace.WithClientTrace(req.Context(), rec.clientTrace())
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if sink != nil {
		sink.record(rec.snapshot(err))
	}
	if debug {
		snap := rec.snapshot(err)
		attrs := []any{
			"host", snap.host,
			"path", snap.path,
			"phase", snap.phase,
			"duration", snap.duration,
			"reused", snap.reused,
		}
		if snap.wasIdle {
			attrs = append(attrs, "idle_ms", snap.idle.Milliseconds())
		}
		if snap.remote != "" {
			attrs = append(attrs, "remote", snap.remote)
		}
		if snap.err != "" {
			attrs = append(attrs, "err", snap.err)
		}
		log.Debug("registry_http", attrs...)
	}
	evictHungHTTP2Conn(req, rec, err, log)
	return resp, err
}

// evictHungHTTP2Conn closes the mux socket after a stream-level header timeout
// so gcr NewRetry cannot reuse it. Does not help if Hub stalls on a heavy tags/list.
func evictHungHTTP2Conn(req *http.Request, rec *httpTripRecorder, err error, log *slog.Logger) {
	if rec == nil || rec.conn == nil || err == nil {
		return
	}
	if req != nil && req.Context().Err() != nil {
		return
	}
	if !isResponseHeaderTimeout(err) {
		return
	}
	remote := rec.remote
	closeHungConn(rec.conn)
	if log == nil {
		log = slog.Default()
	}
	log.Debug("evicted hung http2 connection",
		"remote", remote,
		"repo", repoFromRegistryPath(rec.path),
	)
}

// closeHungConn tears down the TCP under a TLS conn. tls.Conn.Close can hang
// if the peer is unresponsive (stdlib forceCloseConn); NetConn().Close does not.
func closeHungConn(c net.Conn) {
	if c == nil {
		return
	}
	if tc, ok := c.(*tls.Conn); ok {
		if nc := tc.NetConn(); nc != nil {
			_ = nc.Close()
			return
		}
	}
	_ = c.Close()
}

func repoFromRegistryPath(path string) string {
	path = strings.TrimPrefix(path, "/v2/")
	if i := strings.Index(path, "/tags/list"); i >= 0 {
		return path[:i]
	}
	if i := strings.Index(path, "/manifests/"); i >= 0 {
		return path[:i]
	}
	if i := strings.Index(path, "/blobs/"); i >= 0 {
		return path[:i]
	}
	return path
}

func (t *hangTraceTransport) CloseIdleConnections() {
	type idleCloser interface {
		CloseIdleConnections()
	}
	if c, ok := t.base.(idleCloser); ok {
		c.CloseIdleConnections()
	}
}

type httpTripRecorder struct {
	start   time.Time
	host    string
	path    string
	phase   string
	dns     time.Time
	connect time.Time
	tls     time.Time
	gotConn time.Time
	wrote   time.Time
	first   time.Time
	reused  bool
	wasIdle bool
	idle    time.Duration
	remote  string
	conn    net.Conn
}

func (r *httpTripRecorder) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			r.dns = time.Now()
			r.phase = "dns"
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			r.phase = "connect"
		},
		ConnectStart: func(_, _ string) {
			if r.connect.IsZero() {
				r.connect = time.Now()
			}
			r.phase = "connect"
		},
		ConnectDone: func(_, addr string, err error) {
			r.remote = addr
			if err != nil {
				r.phase = "connect"
				return
			}
			r.phase = "tls"
		},
		TLSHandshakeStart: func() {
			r.tls = time.Now()
			r.phase = "tls"
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err != nil {
				r.phase = "tls"
				return
			}
			r.phase = "got_conn"
		},
		GotConn: func(info httptrace.GotConnInfo) {
			r.gotConn = time.Now()
			r.reused = info.Reused
			r.wasIdle = info.WasIdle
			r.idle = info.IdleTime
			if info.Conn != nil {
				r.conn = info.Conn
				r.remote = info.Conn.RemoteAddr().String()
			}
			r.phase = "got_conn"
		},
		WroteRequest: func(httptrace.WroteRequestInfo) {
			r.wrote = time.Now()
			r.phase = "waiting_headers"
		},
		GotFirstResponseByte: func() {
			r.first = time.Now()
			r.phase = "reading_body"
		},
	}
}

func (r *httpTripRecorder) snapshot(err error) httpTripSnapshot {
	phase := r.phase
	if phase == "" {
		phase = "dial"
	}
	snap := httpTripSnapshot{
		host:     r.host,
		path:     r.path,
		phase:    phase,
		duration: time.Since(r.start),
		reused:   r.reused,
		wasIdle:  r.wasIdle,
		idle:     r.idle,
		remote:   r.remote,
	}
	if err != nil {
		snap.err = sanitizeRegistryErr(err)
	}
	return snap
}
