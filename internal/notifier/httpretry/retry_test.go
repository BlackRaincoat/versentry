package httpretry

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoSuccessAfter502(t *testing.T) {
	var calls atomic.Int32
	cfg := Config{MaxAttempts: 3, RetryDelay: time.Millisecond, Sleep: SleepContext}

	err := Do(context.Background(), cfg, DefaultClassifier, func(ctx context.Context) AttemptResult {
		if calls.Add(1) < 3 {
			return AttemptResult{StatusCode: http.StatusBadGateway}
		}
		return AttemptResult{StatusCode: http.StatusOK}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestDoNoRetryOn401(t *testing.T) {
	var calls atomic.Int32
	cfg := Config{MaxAttempts: 3, RetryDelay: time.Millisecond, Sleep: SleepContext}

	err := Do(context.Background(), cfg, DefaultClassifier, func(ctx context.Context) AttemptResult {
		calls.Add(1)
		return AttemptResult{StatusCode: http.StatusUnauthorized}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestDo429RespectsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	var slept time.Duration
	cfg := Config{
		MaxAttempts: 3,
		RetryDelay:  time.Millisecond,
		Sleep: func(ctx context.Context, d time.Duration) error {
			slept = d
			return nil
		},
	}

	err := Do(context.Background(), cfg, DefaultClassifier, func(ctx context.Context) AttemptResult {
		if calls.Add(1) == 1 {
			return AttemptResult{StatusCode: http.StatusTooManyRequests, RetryAfter: "2"}
		}
		return AttemptResult{StatusCode: http.StatusOK}
	})
	if err != nil {
		t.Fatal(err)
	}
	if slept != 2*time.Second {
		t.Fatalf("slept %v", slept)
	}
}

func TestDo429ExceedsCapFailsFast(t *testing.T) {
	var calls atomic.Int32
	cfg := Config{MaxAttempts: 3, RetryDelay: time.Millisecond, Sleep: SleepContext}

	err := Do(context.Background(), cfg, DefaultClassifier, func(ctx context.Context) AttemptResult {
		calls.Add(1)
		return AttemptResult{StatusCode: http.StatusTooManyRequests, RetryAfter: "120"}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestDoContextCanceledDuringSleep(t *testing.T) {
	cfg := Config{
		MaxAttempts: 3,
		RetryDelay:  time.Hour,
		Sleep:       SleepContext,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Do(ctx, cfg, DefaultClassifier, func(ctx context.Context) AttemptResult {
		return AttemptResult{StatusCode: http.StatusBadGateway}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
