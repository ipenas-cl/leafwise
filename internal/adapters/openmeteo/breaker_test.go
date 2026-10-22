package openmeteo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ipenas-cl/leafwise/internal/domain"
)

func TestBreakerOpensFailsFastAndRecovers(t *testing.T) {
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	up := &flaky{fail: true}
	b := &Breaker{Next: up, Failures: 3, Cooldown: time.Minute, Now: func() time.Time { return now }}
	ctx, loc := context.Background(), domain.Location{}
	for i := 0; i < 3; i++ {
		b.Daily(ctx, loc, now, now)
	}
	if b.State() != "open" {
		t.Fatalf("want open, got %s", b.State())
	}
	if _, err := b.Daily(ctx, loc, now, now); !errors.Is(err, ErrCircuitOpen) || up.calls != 3 {
		t.Fatalf("open breaker must not call upstream: calls=%d err=%v", up.calls, err)
	}
	now = now.Add(2 * time.Minute)
	if b.State() != "half-open" {
		t.Fatalf("want half-open, got %s", b.State())
	}
	up.fail = false
	if _, err := b.Daily(ctx, loc, now, now); err != nil || b.State() != "closed" {
		t.Fatalf("trial success should close: %v %s", err, b.State())
	}
}
