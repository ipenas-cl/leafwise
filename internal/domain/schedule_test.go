package domain

import (
	"strings"
	"testing"
	"time"
)

var today = time.Date(2026, 10, 22, 15, 0, 0, 0, time.UTC) // spring in Santiago

func d(offset int) time.Time { return Day(today).AddDate(0, 0, offset) }

func santiago() Location { return Location{Label: "Santiago", Latitude: -33.45, Longitude: -70.66} }

func plant(species string, pl Placement, ex Exposure, lastWatered int) Plant {
	return Plant{ID: "p1", Nickname: "x", Species: species, Placement: pl, PotDiameterCM: 20,
		Exposure: ex, Location: santiago(), OwnerEmail: "a@b.c", LastWatered: d(lastWatered),
		LastFertilized: d(-3)}
}

// dryWeek returns constant weather from -14 to +6 days.
func weather(et0, rain, tmax float64) []WeatherDay {
	var out []WeatherDay
	for i := -14; i <= 6; i++ {
		out = append(out, WeatherDay{Date: d(i), ET0MM: et0, PrecipMM: rain, TMaxC: tmax, TMinC: 10})
	}
	return out
}

func mustProfile(t *testing.T, s string) CareProfile {
	t.Helper()
	p, ok := LookupProfile(s)
	if !ok {
		t.Fatalf("missing profile %s", s)
	}
	return p
}

func TestIndoorPotIgnoresOutdoorWeather(t *testing.T) {
	prof := mustProfile(t, "monstera-deliciosa")
	p := plant("monstera-deliciosa", Pot, Indoor, 0)
	hot := PlanCare(p, prof, weather(7, 0, 38), today)
	rainy := PlanCare(p, prof, weather(1, 40, 15), today)
	if !hot.NextWater.Due.Equal(rainy.NextWater.Due) {
		t.Fatalf("indoor plan should not depend on weather: %v vs %v", hot.NextWater.Due, rainy.NextWater.Due)
	}
	// threshold 30*0.6=18mm, loss 1.2*0.7*1.4667=1.232mm/day -> 15 days, capped at 14.
	if got := hot.NextWater.Due; !got.Equal(d(14)) {
		t.Fatalf("expected max interval cap at +14d, got %v", got)
	}
}

func TestHotDryWeatherDuesSooner(t *testing.T) {
	prof := mustProfile(t, "solanum-lycopersicum")
	p := plant("solanum-lycopersicum", Ground, Outdoor, 0)
	mild := PlanCare(p, prof, weather(3, 0, 22), today)
	hot := PlanCare(p, prof, weather(6, 0, 35), today)
	if !hot.NextWater.Due.Before(mild.NextWater.Due) {
		t.Fatalf("heat should bring watering forward: hot=%v mild=%v", hot.NextWater.Due, mild.NextWater.Due)
	}
}

func TestRainPostponesOutdoorWatering(t *testing.T) {
	prof := mustProfile(t, "rosa")
	p := plant("rosa", Ground, Outdoor, 0)
	dry := PlanCare(p, prof, weather(8, 0, 25), today)
	w := weather(8, 0, 25)
	for i := range w {
		if w[i].Date.Equal(d(3)) {
			w[i].PrecipMM = 25
		}
	}
	wet := PlanCare(p, prof, w, today)
	if !wet.NextWater.Due.After(dry.NextWater.Due) {
		t.Fatalf("rain should postpone watering: wet=%v dry=%v", wet.NextWater.Due, dry.NextWater.Due)
	}
}

func TestLightRainIsIntercepted(t *testing.T) {
	p := plant("rosa", Ground, Outdoor, 0)
	if got := EffectiveRain(p, WeatherDay{PrecipMM: 1.5}, true); got != 0 {
		t.Fatalf("1.5 mm drizzle should not count, got %v", got)
	}
	p.Placement = Pot
	if got := EffectiveRain(p, WeatherDay{PrecipMM: 12}, true); got != 5 {
		t.Fatalf("pot should catch 50%% of 10 mm, got %v", got)
	}
}

func TestOverdueWateringIsDueToday(t *testing.T) {
	prof := mustProfile(t, "ocimum-basilicum")
	p := plant("ocimum-basilicum", Pot, Outdoor, -10)
	plan := PlanCare(p, prof, weather(4, 0, 26), today)
	if !plan.NextWater.Overdue || !plan.NextWater.Due.Equal(d(0)) {
		t.Fatalf("expected overdue today, got %+v", plan.NextWater)
	}
	if !strings.Contains(plan.NextWater.Reason, "overdue since") {
		t.Fatalf("reason should explain overdue: %q", plan.NextWater.Reason)
	}
}

func TestSmallPotDriesFaster(t *testing.T) {
	prof := mustProfile(t, "ocimum-basilicum")
	small := plant("ocimum-basilicum", Pot, Outdoor, 0)
	small.PotDiameterCM = 10
	big := small
	big.PotDiameterCM = 40
	ws := weather(4, 0, 24)
	if s, b := PlanCare(small, prof, ws, today), PlanCare(big, prof, ws, today); s.NextWater.Due.After(b.NextWater.Due) {
		t.Fatalf("small pot should not be later than big pot: %v vs %v", s.NextWater.Due, b.NextWater.Due)
	}
	if potFactor(small) <= potFactor(big) {
		t.Fatal("small pot factor should be larger")
	}
}

func TestMinIntervalRespected(t *testing.T) {
	prof := mustProfile(t, "dracaena-trifasciata")
	p := plant("dracaena-trifasciata", Pot, Outdoor, 0)
	plan := PlanCare(p, prof, weather(12, 0, 40), today)
	if plan.NextWater.Due.Before(d(prof.MinIntervalDays)) {
		t.Fatalf("watered before min interval: %v", plan.NextWater.Due)
	}
}

func TestExtrapolatesBeyondForecast(t *testing.T) {
	prof := mustProfile(t, "lavandula")
	p := plant("lavandula", Ground, Outdoor, 0)
	plan := PlanCare(p, prof, weather(2, 0, 20), today)
	if !plan.NextWater.Due.After(d(6)) || plan.NextWater.Due.After(d(prof.MaxIntervalDays)) {
		t.Fatalf("expected projection between forecast end and max interval, got %v", plan.NextWater.Due)
	}
	if !strings.Contains(plan.NextWater.Reason, "projected") && !strings.Contains(plan.NextWater.Reason, "maximum") {
		t.Fatalf("unexpected reason %q", plan.NextWater.Reason)
	}
}

func TestDeterministic(t *testing.T) {
	prof := mustProfile(t, "citrus-limon")
	p := plant("citrus-limon", Pot, Outdoor, -2)
	a := PlanCare(p, prof, weather(4, 3, 28), today)
	b := PlanCare(p, prof, weather(4, 3, 28), today)
	if a != b {
		t.Fatal("PlanCare must be deterministic")
	}
}

func TestGrowingSeasonByHemisphere(t *testing.T) {
	july := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if !GrowingSeason(july, false) || GrowingSeason(july, true) {
		t.Fatal("July: growing in the north, dormant in the south")
	}
}

func TestFertilizeDormantSouthernWinter(t *testing.T) {
	prof := mustProfile(t, "rosa")
	p := plant("rosa", Ground, Outdoor, 0)
	winter := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	p.LastFertilized = winter.AddDate(0, 0, -40)
	plan := PlanCare(p, prof, nil, winter)
	if want := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC); !plan.NextFertilize.Due.Equal(want) {
		t.Fatalf("expected resume on %v, got %v (%s)", want, plan.NextFertilize.Due, plan.NextFertilize.Reason)
	}
}

func TestFertilizeDormantFeederHalfFrequency(t *testing.T) {
	prof := mustProfile(t, "citrus-limon")
	p := plant("citrus-limon", Pot, Outdoor, 0)
	winter := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	p.LastFertilized = winter.AddDate(0, 0, -35)
	plan := PlanCare(p, prof, nil, winter)
	if want := Day(p.LastFertilized).AddDate(0, 0, 60); !plan.NextFertilize.Due.Equal(want) {
		t.Fatalf("expected %v, got %v", want, plan.NextFertilize.Due)
	}
}

func TestFertilizeShiftsAwayFromHeavyRain(t *testing.T) {
	prof := mustProfile(t, "solanum-lycopersicum")
	p := plant("solanum-lycopersicum", Ground, Outdoor, 0)
	p.LastFertilized = d(-12) // due at +2
	w := weather(3, 0, 22)
	for i := range w {
		if w[i].Date.Equal(d(2)) {
			w[i].PrecipMM = 30
		}
	}
	plan := PlanCare(p, prof, w, today)
	if !plan.NextFertilize.Due.Equal(d(3)) || !strings.Contains(plan.NextFertilize.Reason, "rain") {
		t.Fatalf("expected shift to +3 due to rain, got %+v", plan.NextFertilize)
	}
}

func TestNeverFertilizedIsDueNowInSeason(t *testing.T) {
	prof := mustProfile(t, "ocimum-basilicum")
	p := plant("ocimum-basilicum", Pot, Indoor, 0)
	p.LastFertilized = time.Time{}
	plan := PlanCare(p, prof, nil, today)
	if !plan.NextFertilize.Due.Equal(d(0)) {
		t.Fatalf("expected today, got %v", plan.NextFertilize.Due)
	}
}

func TestPlantValidation(t *testing.T) {
	ok := plant("rosa", Ground, Outdoor, 0)
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Plant){
		"species":   func(p *Plant) { p.Species = "triffid" },
		"placement": func(p *Plant) { p.Placement = "hanging" },
		"exposure":  func(p *Plant) { p.Exposure = "space" },
		"email":     func(p *Plant) { p.OwnerEmail = "nope" },
		"lat":       func(p *Plant) { p.Location.Latitude = 100 },
		"nickname":  func(p *Plant) { p.Nickname = " " },
		"watered":   func(p *Plant) { p.LastWatered = time.Time{} },
	}
	for name, mut := range cases {
		p := ok
		mut(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestPrimaryFinding(t *testing.T) {
	a := Assessment{Findings: []Finding{{Condition: Pest, Confidence: 0.4}, {Condition: Overwatering, Confidence: 0.8}}}
	if a.Primary().Condition != Overwatering || !a.NeedsAttention() {
		t.Fatal("primary should be the most confident finding")
	}
	if (Assessment{}).NeedsAttention() {
		t.Fatal("empty assessment is healthy")
	}
}
