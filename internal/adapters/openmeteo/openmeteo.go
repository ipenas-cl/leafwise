// Package openmeteo implements the WeatherProvider port with the free
// Open-Meteo API (https://open-meteo.com). No API key is required.
package openmeteo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/ipenas-cl/leafwise/internal/domain"
)

// Client calls the Open-Meteo forecast endpoint, which also returns recent
// observed days via past_days.
type Client struct {
	BaseURL string // default https://api.open-meteo.com
	HTTP    *http.Client
	Now     func() time.Time
}

type response struct {
	Daily struct {
		Time   []string   `json:"time"`
		Precip []*float64 `json:"precipitation_sum"`
		ET0    []*float64 `json:"et0_fao_evapotranspiration"`
		TMax   []*float64 `json:"temperature_2m_max"`
		TMin   []*float64 `json:"temperature_2m_min"`
	} `json:"daily"`
}

func (c *Client) Daily(ctx context.Context, loc domain.Location, from, to time.Time) ([]domain.WeatherDay, error) {
	base := c.BaseURL
	if base == "" {
		base = "https://api.open-meteo.com"
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	today := domain.Day(now())
	past := max(0, int(today.Sub(domain.Day(from)).Hours()/24))
	fc := max(1, int(domain.Day(to).Sub(today).Hours()/24)+1)
	q := url.Values{
		"latitude":      {fmt.Sprintf("%.3f", loc.Latitude)},
		"longitude":     {fmt.Sprintf("%.3f", loc.Longitude)},
		"daily":         {"precipitation_sum,et0_fao_evapotranspiration,temperature_2m_max,temperature_2m_min"},
		"past_days":     {fmt.Sprint(min(past, 92))},
		"forecast_days": {fmt.Sprint(min(fc, 16))},
		"timezone":      {"UTC"},
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(200*attempt*attempt) * time.Millisecond):
			}
		}
		days, retry, err := c.fetch(ctx, client, base+"/v1/forecast?"+q.Encode())
		if err == nil {
			return days, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return nil, lastErr
}

func (c *Client) fetch(ctx context.Context, client *http.Client, u string) ([]domain.WeatherDay, bool, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 || resp.StatusCode == 429 {
		return nil, true, fmt.Errorf("open-meteo: status %d", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, false, fmt.Errorf("open-meteo: status %d", resp.StatusCode)
	}
	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, false, err
	}
	val := func(xs []*float64, i int) (float64, bool) {
		if i < len(xs) && xs[i] != nil {
			return *xs[i], true
		}
		return 0, false
	}
	out := make([]domain.WeatherDay, 0, len(r.Daily.Time))
	for i, ds := range r.Daily.Time {
		t, err := time.Parse("2006-01-02", ds)
		if err != nil {
			continue
		}
		et0, ok := val(r.Daily.ET0, i)
		if !ok {
			continue // a day without ET0 is treated as unknown by the planner
		}
		p, _ := val(r.Daily.Precip, i)
		hi, _ := val(r.Daily.TMax, i)
		lo, _ := val(r.Daily.TMin, i)
		out = append(out, domain.WeatherDay{Date: t, PrecipMM: p, ET0MM: et0, TMaxC: hi, TMinC: lo})
	}
	return out, false, nil
}

// Cached decorates a provider with a TTL cache keyed by ~1 km grid cell, so
// a garden full of plants costs one API call per refresh.
type Cached struct {
	Next interface {
		Daily(context.Context, domain.Location, time.Time, time.Time) ([]domain.WeatherDay, error)
	}
	TTL time.Duration

	mu    sync.Mutex
	items map[string]cacheItem
}

type cacheItem struct {
	at   time.Time
	days []domain.WeatherDay
}

func (c *Cached) Daily(ctx context.Context, loc domain.Location, from, to time.Time) ([]domain.WeatherDay, error) {
	key := fmt.Sprintf("%.2f,%.2f,%s,%s", loc.Latitude, loc.Longitude, from.Format("20060102"), to.Format("20060102"))
	c.mu.Lock()
	if c.items == nil {
		c.items = map[string]cacheItem{}
	}
	it, ok := c.items[key]
	c.mu.Unlock()
	if ok && time.Since(it.at) < c.TTL {
		return it.days, nil
	}
	days, err := c.Next.Daily(ctx, loc, from, to)
	if err != nil {
		if ok { // serve stale on error
			return it.days, nil
		}
		return nil, err
	}
	c.mu.Lock()
	c.items[key] = cacheItem{time.Now(), days}
	c.mu.Unlock()
	return days, nil
}

// Static is a deterministic weather provider for demo mode and tests: a mild
// spring with a rainy spell, regardless of location.
type Static struct{ Now func() time.Time }

func (s Static) Daily(_ context.Context, _ domain.Location, from, to time.Time) ([]domain.WeatherDay, error) {
	var out []domain.WeatherDay
	for d, i := domain.Day(from), 0; !d.After(domain.Day(to)); d, i = d.AddDate(0, 0, 1), i+1 {
		w := domain.WeatherDay{Date: d, ET0MM: 3.2 + float64(i%4)*0.6, TMaxC: 21 + float64(i%5), TMinC: 9}
		if i%9 == 3 {
			w.PrecipMM = 14
		}
		out = append(out, w)
	}
	return out, nil
}
