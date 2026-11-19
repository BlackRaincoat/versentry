package core

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func jitterLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func fixedDelay(d time.Duration) func(time.Duration) time.Duration {
	return func(time.Duration) time.Duration { return d }
}

func TestJitterOffReturnsSource(t *testing.T) {
	in := make(chan time.Time)
	log, _ := jitterLog()
	out, _ := jitterSchedule(context.Background(), in, 0, nil, log)
	if out != (<-chan time.Time)(in) {
		t.Fatal("jitter 0 must not wrap the schedule")
	}
}

func TestJitterDelaysTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := make(chan time.Time, 1)
	log, buf := jitterLog()
	out, _ := jitterSchedule(ctx, in, time.Minute, fixedDelay(50*time.Millisecond), log)

	start := time.Now()
	in <- start
	select {
	case <-out:
		if el := time.Since(start); el < 40*time.Millisecond {
			t.Fatalf("forwarded after %v, want ~50ms delay", el)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tick never forwarded")
	}
	if !strings.Contains(buf.String(), "scheduled check delayed by jitter") {
		t.Fatalf("missing INFO: %s", buf.String())
	}
}

func TestJitterDropsTicksWhilePending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := make(chan time.Time)
	log, _ := jitterLog()
	out, _ := jitterSchedule(ctx, in, time.Minute, fixedDelay(80*time.Millisecond), log)

	in <- time.Now()
	in <- time.Now() // arrives during the pending delay: dropped
	<-out
	select {
	case <-out:
		t.Fatal("extra tick during the delay must be dropped")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestJitterSkipCancelsPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := make(chan time.Time)
	log, _ := jitterLog()
	out, skip := jitterSchedule(ctx, in, time.Minute, fixedDelay(100*time.Millisecond), log)

	in <- time.Now()
	time.Sleep(10 * time.Millisecond)
	skip() // SIGUSR1 ran the check already
	select {
	case <-out:
		t.Fatal("cancelled tick must not be forwarded")
	case <-time.After(200 * time.Millisecond):
	}

	// A stale skip (nothing pending) must not swallow the next tick.
	skip()
	in <- time.Now()
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatal("next tick lost to a stale skip")
	}
}

func TestJitterStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan time.Time)
	log, _ := jitterLog()
	out, _ := jitterSchedule(ctx, in, time.Minute, fixedDelay(time.Hour), log)
	in <- time.Now()
	cancel()
	select {
	case <-out:
		t.Fatal("no tick after cancel")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRandomDelayRange(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if d := randomDelay(time.Second); d < 0 || d >= time.Second {
			t.Fatalf("delay %v outside [0, 1s)", d)
		}
	}
	if randomDelay(0) != 0 {
		t.Fatal("max 0 → 0")
	}
}
