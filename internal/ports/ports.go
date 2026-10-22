// Package ports declares the interfaces between the application core and the
// outside world. Driven adapters (Postgres, S3, Open-Meteo, LLMs, SMTP...)
// implement these; the core never imports an adapter.
package ports

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/ipenas-cl/leafwise/internal/domain"
)

// PlantRepository persists plants.
type PlantRepository interface {
	Save(ctx context.Context, p domain.Plant) error
	Get(ctx context.Context, id string) (domain.Plant, error)
	List(ctx context.Context) ([]domain.Plant, error)
	Delete(ctx context.Context, id string) error
}

// DiagnosisRepository persists photo diagnoses.
type DiagnosisRepository interface {
	SaveDiagnosis(ctx context.Context, d domain.Diagnosis) error
	ListDiagnoses(ctx context.Context, plantID string, limit int) ([]domain.Diagnosis, error)
}

// ReminderLog makes reminders idempotent across restarts and replicas.
// MarkSent returns false when the reminder was already sent.
type ReminderLog interface {
	MarkSent(ctx context.Context, plantID string, kind domain.TaskKind, due time.Time) (bool, error)
}

// PhotoStore keeps uploaded plant photos (S3-compatible in production).
type PhotoStore interface {
	Put(ctx context.Context, key, contentType string, r io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, string, error)
}

// WeatherProvider returns daily observed and forecast weather.
type WeatherProvider interface {
	Daily(ctx context.Context, loc domain.Location, from, to time.Time) ([]domain.WeatherDay, error)
}

// DiagnosisInput is what a diagnoser gets to look at.
type DiagnosisInput struct {
	Image      []byte
	MIMEType   string
	Species    string
	CommonName string
	Placement  domain.Placement
	Exposure   domain.Exposure
}

// Diagnoser is the "diagnosis port". Adapters must return JSON that matches
// api/diagnosis.schema.json; the core validates it before trusting it.
type Diagnoser interface {
	Name() string
	Diagnose(ctx context.Context, in DiagnosisInput) (json.RawMessage, error)
}

// Notification is a channel-agnostic message to a gardener.
type Notification struct {
	To      string
	Subject string
	Body    string
	PlantID string
}

// Notifier delivers notifications (email, push, ...).
type Notifier interface {
	Name() string
	Notify(ctx context.Context, n Notification) error
}

// Locker provides a cluster-wide lock so only one replica runs a job.
type Locker interface {
	TryLock(ctx context.Context, name string) (unlock func(), ok bool, err error)
}

// Clock is injected so scheduling is testable.
type Clock interface{ Now() time.Time }

// SystemClock is the real clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }
