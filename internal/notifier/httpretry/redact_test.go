package httpretry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

const secretURL = "https://user:pass@hooks.example.com/services/T000/B000/XXXSECRET?token=abc#frag"

func dialRefused() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

func assertNoSecrets(t *testing.T, msg string) {
	t.Helper()
	for _, s := range []string{"/services", "XXXSECRET", "token=abc", "user:pass", "frag"} {
		if strings.Contains(msg, s) {
			t.Fatalf("message leaked %q: %s", s, msg)
		}
	}
}

func TestRedactURLErrorKeepsOpHostAndCause(t *testing.T) {
	err := RedactURLError(&url.Error{Op: "Post", URL: secretURL, Err: dialRefused()})
	msg := err.Error()
	assertNoSecrets(t, msg)
	if want := `Post https://hooks.example.com: dial tcp: connection refused`; msg != want {
		t.Fatalf("msg = %q, want %q", msg, want)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatal("errors.Is lost the cause")
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Fatal("errors.As lost *net.OpError")
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		t.Fatal("*url.Error still in chain")
	}
}

func TestRedactURLErrorTimeoutSemantics(t *testing.T) {
	err := RedactURLError(&url.Error{Op: "Post", URL: secretURL, Err: context.DeadlineExceeded})
	assertNoSecrets(t, err.Error())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("errors.Is(DeadlineExceeded) lost")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatal("Timeout() lost")
	}
}

func TestRedactURLErrorWrapped(t *testing.T) {
	inner := &url.Error{Op: "parse", URL: secretURL, Err: errors.New("invalid control character in URL")}
	err := RedactURLError(fmt.Errorf("create request: %w", inner))
	msg := err.Error()
	assertNoSecrets(t, msg)
	if !strings.HasPrefix(msg, `create request: parse https://hooks.example.com: `) {
		t.Fatalf("msg = %q", msg)
	}
}

func TestRedactURLErrorUnparseableURL(t *testing.T) {
	err := RedactURLError(&url.Error{Op: "parse", URL: "::/secret\x7f", Err: errors.New("bad")})
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("leaked: %s", err)
	}
}

func TestRedactURLErrorPassthrough(t *testing.T) {
	if RedactURLError(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	plain := errors.New("read response: EOF")
	if RedactURLError(plain) != plain {
		t.Fatal("non-url error must be returned as-is")
	}
	once := RedactURLError(&url.Error{Op: "Post", URL: secretURL, Err: dialRefused()})
	if twice := RedactURLError(once); twice != once {
		t.Fatal("redaction must be idempotent")
	}
}

func TestDefaultClassifierNetworkErrorRedactedAndRetryable(t *testing.T) {
	out := DefaultClassifier(0, nil, "", &url.Error{Op: "Post", URL: secretURL, Err: dialRefused()})
	if !out.Retryable {
		t.Fatal("dial error must stay retryable")
	}
	assertNoSecrets(t, out.Err.Error())

	out = DefaultClassifier(0, nil, "", &url.Error{Op: "Post", URL: secretURL, Err: context.Canceled})
	if out.Retryable {
		t.Fatal("canceled must not be retryable")
	}
	if !errors.Is(out.Err, context.Canceled) {
		t.Fatal("errors.Is(Canceled) lost")
	}
	assertNoSecrets(t, out.Err.Error())
}

func TestDoRedactsBeforeCustomClassifier(t *testing.T) {
	var seen error
	classify := func(status int, body []byte, ra string, netErr error) Outcome {
		seen = netErr
		return Outcome{Err: fmt.Errorf("send request: %w", netErr)}
	}
	err := Do(context.Background(), Config{MaxAttempts: 1}, classify, func(ctx context.Context) AttemptResult {
		return AttemptResult{Err: &url.Error{Op: "Post", URL: secretURL, Err: dialRefused()}}
	})
	assertNoSecrets(t, seen.Error())
	assertNoSecrets(t, err.Error())
}
