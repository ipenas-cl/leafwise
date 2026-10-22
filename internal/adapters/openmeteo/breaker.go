package openmeteo

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ipenas-cl/leafwise/internal/domain"
)

// ErrCircuitOpen is returned while the breaker is rejecting calls.
var ErrCircuitOpen = errors.New("weather circuit open")

// Breaker is a small circuit breaker. After Failures consecutive errors it
// opens for Cooldown and fails fast, so a dead upstream costs nothing per
// request instead of a timeout. After the cooldown a single trial call is
// let through (half-open); success closes the circuit.
type Breaker struct {
	Next interface {
		Daily(context.Context, domain.Location, time.Time, time.Time) ([]domain.WeatherDay, error)
	}
	Failures int
	Cooldown time.Duration
	Now      func() time.Time

	mu        sync.Mutex
	fails     int
	openUntil time.Time
	trial     bool
}

func (b *Breaker) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// State reports "closed", "open" or "half-open".
func (b *Breaker) State() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.fails < b.Failures:
		return "closed"
	case b.now().Before(b.openUntil):
		return "open"
	default:
		return "half-open"
	}
}

func (b *Breaker) Daily(ctx context.Context, loc domain.Location, from, to time.Time) ([]domain.WeatherDay, error) {
	b.mu.Lock()
	if b.fails >= b.Failures {
		if b.now().Before(b.openUntil) || b.trial {
			b.mu.Unlock()
			return nil, ErrCircuitOpen
		}
		b.trial = true
	}
	b.mu.Unlock()

	days, err := b.Next.Daily(ctx, loc, from, to)

	b.mu.Lock()
	defer b.mu.Unlock()
	b.trial = false
	if err != nil {
		b.fails++
		if b.fails >= b.Failures {
			b.openUntil = b.now().Add(b.Cooldown)
		}
		return nil, err
	}
	b.fails = 0
	return days, nil
}
