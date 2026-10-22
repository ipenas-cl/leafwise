package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ipenas-cl/leafwise/internal/domain"
)

// Integration test: runs when LEAFWISE_TEST_DATABASE_URL points at a
// throwaway Postgres (CI starts one as a service container).
func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("LEAFWISE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LEAFWISE_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	p := domain.Plant{ID: "it-" + now.Format("150405.000"), Nickname: "Rose", Species: "rosa", Placement: domain.Ground,
		Exposure: domain.Outdoor, Location: domain.Location{Label: "yard", Latitude: -33, Longitude: -70},
		OwnerEmail: "a@b.c", LastWatered: now, CreatedAt: now}
	if err := s.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, p.ID)
	if err != nil || got.Nickname != "Rose" || !got.LastFertilized.IsZero() || !got.LastWatered.Equal(now) {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	d := domain.Diagnosis{ID: p.ID + "-d", PlantID: p.ID, PhotoKey: "k", Provider: "mock", CreatedAt: now,
		Assessment: domain.Assessment{Summary: "ok", Findings: []domain.Finding{{Condition: domain.Healthy, Label: "fine", Confidence: 0.9}}}}
	if err := s.SaveDiagnosis(ctx, d); err != nil {
		t.Fatal(err)
	}
	ds, err := s.ListDiagnoses(ctx, p.ID, 10)
	if err != nil || len(ds) != 1 || ds[0].Findings[0].Label != "fine" {
		t.Fatalf("diagnoses: %+v %v", ds, err)
	}
	first, _ := s.MarkSent(ctx, p.ID, domain.Water, now)
	again, _ := s.MarkSent(ctx, p.ID, domain.Water, now)
	if !first || again {
		t.Fatalf("MarkSent must be idempotent: %v %v", first, again)
	}
	unlock, ok, err := s.TryLock(ctx, "test")
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	if _, ok2, _ := s.TryLock(ctx, "test"); ok2 {
		t.Fatal("second lock must fail while held")
	}
	unlock()
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}
