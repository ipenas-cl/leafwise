// Package domain holds Leafwise's core model and pure business rules.
// It has no dependencies on infrastructure: no SQL, no HTTP, no clocks.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Placement is where the roots live. Pots dry out faster than open ground.
type Placement string

const (
	Pot    Placement = "pot"
	Ground Placement = "ground"
)

// Exposure says whether the plant sees the sky (rain, sun, wind) or not.
type Exposure string

const (
	Indoor  Exposure = "indoor"
	Outdoor Exposure = "outdoor"
)

// Location is used to fetch weather and to know the hemisphere (seasons).
type Location struct {
	Label     string  `json:"label"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// SouthernHemisphere reports whether seasons are flipped for this location.
func (l Location) SouthernHemisphere() bool { return l.Latitude < 0 }

// Plant is the aggregate root of the gardening domain.
type Plant struct {
	ID             string    `json:"id"`
	Nickname       string    `json:"nickname"`
	Species        string    `json:"species"`
	Placement      Placement `json:"placement"`
	PotDiameterCM  int       `json:"potDiameterCm,omitempty"`
	Exposure       Exposure  `json:"exposure"`
	Location       Location  `json:"location"`
	OwnerEmail     string    `json:"ownerEmail"`
	LastWatered    time.Time `json:"lastWatered"`
	LastFertilized time.Time `json:"lastFertilized,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

// ErrValidation wraps every user-input error so adapters can map it to 400.
var ErrValidation = errors.New("validation error")

// ErrNotFound is returned by repositories when an entity does not exist.
var ErrNotFound = errors.New("not found")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, a...))
}

// Validate enforces the invariants of a plant registration.
func (p Plant) Validate() error {
	if strings.TrimSpace(p.Nickname) == "" {
		return invalid("nickname is required")
	}
	if _, ok := LookupProfile(p.Species); !ok {
		return invalid("unknown species %q", p.Species)
	}
	switch p.Placement {
	case Pot:
		if p.PotDiameterCM < 0 || p.PotDiameterCM > 200 {
			return invalid("potDiameterCm must be between 0 and 200")
		}
	case Ground:
	default:
		return invalid("placement must be %q or %q", Pot, Ground)
	}
	if p.Exposure != Indoor && p.Exposure != Outdoor {
		return invalid("exposure must be %q or %q", Indoor, Outdoor)
	}
	if p.Location.Latitude < -90 || p.Location.Latitude > 90 || p.Location.Longitude < -180 || p.Location.Longitude > 180 {
		return invalid("location coordinates out of range")
	}
	if !strings.Contains(p.OwnerEmail, "@") {
		return invalid("ownerEmail must be a valid email")
	}
	if p.LastWatered.IsZero() {
		return invalid("lastWatered is required")
	}
	return nil
}

// Day truncates a time to a calendar day in UTC. All scheduling math is
// done on whole days so results are stable regardless of time of request.
func Day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
