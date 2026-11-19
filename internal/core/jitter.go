package core

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"
)

// randomDelay returns a uniform delay in [0, max).
func randomDelay(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return rand.N(max)
}

// jitterSchedule delays every tick from in by a random duration in [0, max)
// before forwarding it, so hosts sharing a schedule do not hit registries at
// the same second. Ticks arriving while a delayed one is pending are dropped.
// skip cancels the pending delayed tick (a forced check just ran). With
// max <= 0 the source is returned unchanged.
func jitterSchedule(
	ctx context.Context,
	in <-chan time.Time,
	max time.Duration,
	delay func(time.Duration) time.Duration,
	log *slog.Logger,
) (out <-chan time.Time, skip func()) {
	if max <= 0 {
		return in, func() {}
	}
	if delay == nil {
		delay = randomDelay
	}
	fwd := make(chan time.Time, 1)
	skipCh := make(chan struct{}, 1)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-in:
			}

			// A skip sent while nothing was pending must not cancel this tick.
			select {
			case <-skipCh:
			default:
			}

			d := delay(max)
			log.Info("scheduled check delayed by jitter", "delay", d.Round(time.Second))
			timer := time.NewTimer(d)
		wait:
			for {
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-skipCh:
					timer.Stop()
					log.Debug("jittered scheduled check cancelled: a forced check just ran")
					break wait
				case <-in:
					// Already have a pending check; drop the extra tick.
				case t := <-timer.C:
					select {
					case fwd <- t:
					default:
					}
					break wait
				}
			}
		}
	}()

	return fwd, func() {
		select {
		case skipCh <- struct{}{}:
		default:
		}
	}
}
