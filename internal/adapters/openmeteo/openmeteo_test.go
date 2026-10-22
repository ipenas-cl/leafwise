package openmeteo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipenas-cl/leafwise/internal/domain"
)

const sample = `{"daily":{"time":["2026-10-21","2026-10-22","2026-10-23"],
"precipitation_sum":[9.5,0.0,null],"et0_fao_evapotranspiration":[4.23,4.28,null],
"temperature_2m_max":[19.8,19.3,17.4],"temperature_2m_min":[8.1,9.0,7.2]}}`

func TestParsesDailyWeather(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(sample))
	}))
	defer srv.Close()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	c := &Client{BaseURL: srv.URL, Now: func() time.Time { return now }}
	days, err := c.Daily(context.Background(), domain.Location{Latitude: -33.45, Longitude: -70.66}, now.AddDate(0, 0, -14), now.AddDate(0, 0, 6))
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 { // the day with null ET0 is dropped
		t.Fatalf("want 2 days, got %d", len(days))
	}
	if days[0].PrecipMM != 9.5 || days[1].ET0MM != 4.28 {
		t.Fatalf("bad parse: %+v", days)
	}
	for _, want := range []string{"past_days=14", "forecast_days=7", "et0_fao_evapotranspiration", "latitude=-33.450"} {
		if !contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

type flaky struct {
	calls int
	fail  bool
}

func (f *flaky) Daily(context.Context, domain.Location, time.Time, time.Time) ([]domain.WeatherDay, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("boom")
	}
	return []domain.WeatherDay{{ET0MM: 3}}, nil
}

func TestCacheServesFreshAndStale(t *testing.T) {
	up := &flaky{}
	c := &Cached{Next: up, TTL: time.Hour}
	loc := domain.Location{Latitude: 1, Longitude: 2}
	t0 := time.Now()
	c.Daily(context.Background(), loc, t0, t0)
	c.Daily(context.Background(), loc, t0, t0)
	if up.calls != 1 {
		t.Fatalf("expected cache hit, upstream called %d times", up.calls)
	}
	c.TTL = 0
	up.fail = true
	days, err := c.Daily(context.Background(), loc, t0, t0)
	if err != nil || len(days) != 1 {
		t.Fatalf("expected stale data on upstream error, got %v %v", days, err)
	}
}

func TestRetriesServerErrors(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(502)
			return
		}
		w.Write([]byte(sample))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	if _, err := c.Daily(context.Background(), domain.Location{}, time.Now(), time.Now()); err != nil || calls != 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
