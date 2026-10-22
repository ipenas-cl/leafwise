// Package app contains the use cases. It orchestrates the domain and talks to
// the outside world only through ports.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/ipenas-cl/leafwise/internal/domain"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

// PastWeatherDays is how much observed history we ask for when planning.
const PastWeatherDays = 14

// ForecastDays is how far ahead we look.
const ForecastDays = 7

// PlantService implements plant registration and care planning.
type PlantService struct {
	Plants  ports.PlantRepository
	Weather ports.WeatherProvider
	Clock   ports.Clock
	Log     *slog.Logger
}

// NewID returns a random, URL-safe identifier.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Register validates and stores a new plant.
func (s *PlantService) Register(ctx context.Context, p domain.Plant) (domain.Plant, error) {
	if p.ID == "" {
		p.ID = NewID()
	}
	if p.LastWatered.IsZero() {
		p.LastWatered = s.Clock.Now()
	}
	p.CreatedAt = s.Clock.Now().UTC()
	if err := p.Validate(); err != nil {
		return domain.Plant{}, err
	}
	return p, s.Plants.Save(ctx, p)
}

func (s *PlantService) Get(ctx context.Context, id string) (domain.Plant, error) {
	return s.Plants.Get(ctx, id)
}

func (s *PlantService) List(ctx context.Context) ([]domain.Plant, error) { return s.Plants.List(ctx) }

func (s *PlantService) Delete(ctx context.Context, id string) error { return s.Plants.Delete(ctx, id) }

// RecordCare marks a plant as watered or fertilized at the given time.
func (s *PlantService) RecordCare(ctx context.Context, id string, kind domain.TaskKind, at time.Time) (domain.Plant, error) {
	p, err := s.Plants.Get(ctx, id)
	if err != nil {
		return p, err
	}
	if at.IsZero() {
		at = s.Clock.Now()
	}
	switch kind {
	case domain.Water:
		p.LastWatered = at.UTC()
	case domain.Fertilize:
		p.LastFertilized = at.UTC()
	default:
		return p, fmt.Errorf("%w: unknown care kind %q", domain.ErrValidation, kind)
	}
	return p, s.Plants.Save(ctx, p)
}

// PlanView is a care plan plus context for the UI.
type PlanView struct {
	domain.CarePlan
	Profile         domain.CareProfile  `json:"profile"`
	Weather         []domain.WeatherDay `json:"weather"`
	WeatherDegraded bool                `json:"weatherDegraded"`
}

// Plan computes the care plan for one plant. If the weather provider is
// down we degrade gracefully to climatology defaults instead of failing.
func (s *PlantService) Plan(ctx context.Context, p domain.Plant) (PlanView, error) {
	prof, ok := domain.LookupProfile(p.Species)
	if !ok {
		return PlanView{}, fmt.Errorf("%w: unknown species", domain.ErrValidation)
	}
	now := s.Clock.Now()
	var weather []domain.WeatherDay
	degraded := false
	if p.Exposure == domain.Outdoor {
		from := domain.Day(now).AddDate(0, 0, -PastWeatherDays)
		to := domain.Day(now).AddDate(0, 0, ForecastDays-1)
		w, err := s.Weather.Daily(ctx, p.Location, from, to)
		if err != nil {
			s.Log.WarnContext(ctx, "weather unavailable, using defaults", "plant", p.ID, "err", err)
			degraded = true
		}
		weather = w
	}
	return PlanView{
		CarePlan:        domain.PlanCare(p, prof, weather, now),
		Profile:         prof,
		Weather:         weather,
		WeatherDegraded: degraded,
	}, nil
}

// CalendarEntry is one task on the care calendar.
type CalendarEntry struct {
	domain.CareTask
	PlantName string `json:"plantName"`
	Species   string `json:"species"`
}

// Calendar projects care tasks for all plants over the next `days` days,
// assuming the gardener follows each recommendation.
func (s *PlantService) Calendar(ctx context.Context, days int) ([]CalendarEntry, error) {
	plants, err := s.Plants.List(ctx)
	if err != nil {
		return nil, err
	}
	end := domain.Day(s.Clock.Now()).AddDate(0, 0, days)
	var out []CalendarEntry
	for _, p := range plants {
		view, err := s.Plan(ctx, p)
		if err != nil {
			continue
		}
		prof := view.Profile
		add := func(t domain.CareTask) {
			out = append(out, CalendarEntry{CareTask: t, PlantName: p.Nickname, Species: prof.CommonName})
		}
		// Project waterings by re-running the pure planner as if each
		// recommendation were followed.
		sim := p
		w := view.NextWater
		for i := 0; i < 15 && w.Due.Before(end); i++ {
			add(w)
			sim.LastWatered = w.Due
			next := domain.PlanCare(sim, prof, view.Weather, s.Clock.Now()).NextWater
			if !next.Due.After(w.Due) {
				break
			}
			w = next
		}
		if view.NextFertilize.Due.Before(end) {
			add(view.NextFertilize)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Due.Equal(out[j].Due) {
			return out[i].Due.Before(out[j].Due)
		}
		return out[i].PlantName < out[j].PlantName
	})
	return out, nil
}
