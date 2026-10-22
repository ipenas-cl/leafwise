package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ipenas-cl/leafwise/internal/adapters/diagnosis"
	"github.com/ipenas-cl/leafwise/internal/adapters/memory"
	"github.com/ipenas-cl/leafwise/internal/adapters/openmeteo"
	"github.com/ipenas-cl/leafwise/internal/app"
	"github.com/ipenas-cl/leafwise/internal/domain"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type recorder struct{ sent []ports.Notification }

func (r *recorder) Name() string { return "rec" }
func (r *recorder) Notify(_ context.Context, n ports.Notification) error {
	r.sent = append(r.sent, n)
	return nil
}

type brokenWeather struct{}

func (brokenWeather) Daily(context.Context, domain.Location, time.Time, time.Time) ([]domain.WeatherDay, error) {
	return nil, errors.New("upstream down")
}

var now = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newPlant() domain.Plant {
	return domain.Plant{Nickname: "Basil", Species: "ocimum-basilicum", Placement: domain.Pot, PotDiameterCM: 15,
		Exposure: domain.Outdoor, Location: domain.Location{Latitude: -33.4, Longitude: -70.6},
		OwnerEmail: "me@example.com", LastWatered: now.AddDate(0, 0, -6), LastFertilized: now.AddDate(0, 0, -30)}
}

func solid(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func TestReminderJobIsIdempotent(t *testing.T) {
	store := memory.NewStore()
	clock := fixedClock{now}
	ps := &app.PlantService{Plants: store, Weather: openmeteo.Static{}, Clock: clock, Log: quiet}
	if _, err := ps.Register(context.Background(), newPlant()); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	job := &app.ReminderJob{Plants: ps, Log: store, Notifier: rec, Locker: &memory.Locker{}, Clock: clock, Logger: quiet}
	n1, err := job.RunOnce(context.Background())
	if err != nil || n1 != 2 {
		t.Fatalf("first run: want 2 reminders (water+feed), got %d err=%v", n1, err)
	}
	n2, _ := job.RunOnce(context.Background())
	if n2 != 0 {
		t.Fatalf("second run must not resend, got %d", n2)
	}
	if rec.sent[0].To != "me@example.com" {
		t.Fatalf("wrong recipient %q", rec.sent[0].To)
	}
}

func TestPlanDegradesWhenWeatherIsDown(t *testing.T) {
	store := memory.NewStore()
	ps := &app.PlantService{Plants: store, Weather: brokenWeather{}, Clock: fixedClock{now}, Log: quiet}
	p, _ := ps.Register(context.Background(), newPlant())
	v, err := ps.Plan(context.Background(), p)
	if err != nil || !v.WeatherDegraded {
		t.Fatalf("expected degraded plan, got err=%v degraded=%v", err, v.WeatherDegraded)
	}
}

func TestCalendarProjectsRecurringWaterings(t *testing.T) {
	store := memory.NewStore()
	ps := &app.PlantService{Plants: store, Weather: openmeteo.Static{}, Clock: fixedClock{now}, Log: quiet}
	p := newPlant()
	p.LastWatered = now
	ps.Register(context.Background(), p)
	cal, err := ps.Calendar(context.Background(), 14)
	if err != nil {
		t.Fatal(err)
	}
	waters := 0
	for i, e := range cal {
		if e.Kind == domain.Water {
			waters++
		}
		if i > 0 && e.Due.Before(cal[i-1].Due) {
			t.Fatal("calendar must be sorted by date")
		}
	}
	if waters < 2 {
		t.Fatalf("expected several projected waterings in 14 days, got %d", waters)
	}
}

func TestRecordCare(t *testing.T) {
	store := memory.NewStore()
	ps := &app.PlantService{Plants: store, Weather: openmeteo.Static{}, Clock: fixedClock{now}, Log: quiet}
	p, _ := ps.Register(context.Background(), newPlant())
	got, err := ps.RecordCare(context.Background(), p.ID, domain.Water, time.Time{})
	if err != nil || !got.LastWatered.Equal(now) {
		t.Fatalf("watering not recorded: %v %v", got.LastWatered, err)
	}
	if _, err := ps.RecordCare(context.Background(), p.ID, "prune", now); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func diagService(d ports.Diagnoser, rec *recorder) (*app.DiagnosisService, *memory.Store, string) {
	store := memory.NewStore()
	ps := &app.PlantService{Plants: store, Weather: openmeteo.Static{}, Clock: fixedClock{now}, Log: quiet}
	p, _ := ps.Register(context.Background(), newPlant())
	return &app.DiagnosisService{Plants: store, Store: store, Photos: memory.NewPhotos(), Diagnoser: d,
		Notifier: rec, Clock: fixedClock{now}, Log: quiet}, store, p.ID
}

func TestDiagnoseWithMockStoresAndNotifies(t *testing.T) {
	rec := &recorder{}
	svc, _, id := diagService(diagnosis.Mock{}, rec)
	d, err := svc.Diagnose(context.Background(), id, solid(color.RGBA{220, 200, 60, 255})) // yellow
	if err != nil {
		t.Fatal(err)
	}
	if d.Primary().Condition != domain.NutrientDeficiency {
		t.Fatalf("yellow leaves should read as nutrient deficiency, got %s", d.Primary().Condition)
	}
	if len(rec.sent) != 1 {
		t.Fatalf("expected an attention notification, got %d", len(rec.sent))
	}
	hist, _ := svc.History(context.Background(), id, 10)
	if len(hist) != 1 || hist[0].PhotoKey == "" {
		t.Fatalf("diagnosis not persisted: %+v", hist)
	}
}

func TestHealthyPlantDoesNotNotify(t *testing.T) {
	rec := &recorder{}
	svc, _, id := diagService(diagnosis.Mock{}, rec)
	d, err := svc.Diagnose(context.Background(), id, solid(color.RGBA{40, 140, 50, 255}))
	if err != nil || d.Primary().Condition != domain.Healthy || len(rec.sent) != 0 {
		t.Fatalf("green plant: cond=%s notified=%d err=%v", d.Primary().Condition, len(rec.sent), err)
	}
}

type rawDiagnoser string

func (r rawDiagnoser) Name() string { return "raw" }
func (r rawDiagnoser) Diagnose(context.Context, ports.DiagnosisInput) (json.RawMessage, error) {
	return json.RawMessage(r), nil
}

func TestSchemaRejectsBadAdapterOutput(t *testing.T) {
	bad := map[string]string{
		"not json":         `sure! here you go`,
		"missing actions":  `{"summary":"x","findings":[{"condition":"pest","label":"a","confidence":0.5,"evidence":""}]}`,
		"bad condition":    `{"summary":"x","findings":[{"condition":"alien","label":"a","confidence":0.5,"evidence":""}],"actions":[]}`,
		"confidence > 1":   `{"summary":"x","findings":[{"condition":"pest","label":"a","confidence":7,"evidence":""}],"actions":[]}`,
		"extra properties": `{"summary":"x","findings":[{"condition":"pest","label":"a","confidence":0.5,"evidence":""}],"actions":[],"hack":1}`,
		"empty findings":   `{"summary":"x","findings":[],"actions":[]}`,
	}
	for name, raw := range bad {
		svc, _, id := diagService(rawDiagnoser(raw), &recorder{})
		_, err := svc.Diagnose(context.Background(), id, solid(color.RGBA{1, 2, 3, 255}))
		if !errors.Is(err, app.ErrInvalidAssessment) {
			t.Errorf("%s: expected ErrInvalidAssessment, got %v", name, err)
		}
	}
}

func TestDiagnoseRejectsNonImages(t *testing.T) {
	svc, _, id := diagService(diagnosis.Mock{}, &recorder{})
	if _, err := svc.Diagnose(context.Background(), id, []byte("%PDF-1.4 not a photo")); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if _, err := svc.Diagnose(context.Background(), "missing", solid(color.RGBA{})); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}
