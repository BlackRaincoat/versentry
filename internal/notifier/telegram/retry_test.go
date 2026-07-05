package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testHTTPNotifier(t *testing.T, apiURL string, maxAttempts int, retryDelay time.Duration) *Notifier {
	t.Helper()
	return &Notifier{
		token:       "123456:TESTTOKEN",
		chatID:      "1",
		parseMode:   "HTML",
		apiURL:      apiURL,
		maxAttempts: maxAttempts,
		retryDelay:  retryDelay,
		sleep:       func(ctx context.Context, d time.Duration) error { return nil },
		client:      &http.Client{Timeout: 5 * time.Second},
	}
}

func TestSendMessageWithRetrySuccessAfter502(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(telegramResponse{OK: false, Description: "bad gateway"})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(telegramResponse{OK: true})
	}))
	defer srv.Close()

	n := testHTTPNotifier(t, srv.URL, 3, time.Millisecond)
	if err := n.sendMessageWithRetry(context.Background(), "hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
}

func TestSendMessageWithRetryNoRetryOn401(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(telegramResponse{
			OK:          false,
			ErrorCode:   401,
			Description: "Unauthorized",
		})
	}))
	defer srv.Close()

	n := testHTTPNotifier(t, srv.URL, 3, time.Millisecond)
	err := n.sendMessageWithRetry(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestSendMessageWithRetry429RespectsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	var slept time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			resp := telegramResponse{
				OK:          false,
				ErrorCode:   429,
				Description: "Too Many Requests",
			}
			resp.Parameters.RetryAfter = 2
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(telegramResponse{OK: true})
	}))
	defer srv.Close()

	n := testHTTPNotifier(t, srv.URL, 3, time.Millisecond)
	n.sleep = func(ctx context.Context, d time.Duration) error {
		slept = d
		return nil
	}

	if err := n.sendMessageWithRetry(context.Background(), "hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slept != 2*time.Second {
		t.Fatalf("slept %v, want 2s", slept)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

func TestSendMessageWithRetry429ExceedsCapFailsFast(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		resp := telegramResponse{
			OK:          false,
			ErrorCode:   429,
			Description: "Too Many Requests",
		}
		resp.Parameters.RetryAfter = 120
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	n := testHTTPNotifier(t, srv.URL, 3, time.Millisecond)
	err := n.sendMessageWithRetry(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestSendMessageWithRetryContextCanceledDuringSleep(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(telegramResponse{OK: false})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	n := testHTTPNotifier(t, srv.URL, 3, time.Millisecond)
	err := n.sendMessageWithRetry(ctx, "hi")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMaxAttemptsFromRetries(t *testing.T) {
	if maxAttemptsFromRetries(0) != 1 {
		t.Fatal("0 retries should mean 1 attempt")
	}
	if maxAttemptsFromRetries(1) != 1 {
		t.Fatal("1 retry should mean 1 attempt")
	}
	if maxAttemptsFromRetries(3) != 3 {
		t.Fatal("3 retries should mean 3 attempts")
	}
}

func TestOptionalIntRetries(t *testing.T) {
	got, err := optionalInt(map[string]any{"retries": 2}, "retries", 3)
	if err != nil || got != 2 {
		t.Fatalf("got %d err %v", got, err)
	}
	_, err = optionalInt(map[string]any{"retries": -1}, "retries", 3)
	if err == nil {
		t.Fatal("expected error for negative retries")
	}
}
