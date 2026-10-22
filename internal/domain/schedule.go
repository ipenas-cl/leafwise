package domain

import (
	"fmt"
	"math"
	"time"
)

// WeatherDay is one day of observed or forecast weather.
type WeatherDay struct {
	Date     time.Time `json:"date"`
	PrecipMM float64   `json:"precipMm"`
	ET0MM    float64   `json:"et0Mm"` // FAO reference evapotranspiration
	TMaxC    float64   `json:"tmaxC"`
	TMinC    float64   `json:"tminC"`
}

// TaskKind is the type of care action.
type TaskKind string

const (
	Water     TaskKind = "water"
	Fertilize TaskKind = "fertilize"
)

// CareTask is a scheduled care action with a human-readable reason.
type CareTask struct {
	PlantID string    `json:"plantId"`
	Kind    TaskKind  `json:"kind"`
	Due     time.Time `json:"due"`
	Overdue bool      `json:"overdue"`
	Reason  string    `json:"reason"`
}

// CarePlan is the output of the scheduler for one plant.
type CarePlan struct {
	PlantID       string   `json:"plantId"`
	DeficitMM     float64  `json:"deficitMm"`   // current soil water deficit
	ThresholdMM   float64  `json:"thresholdMm"` // deficit that triggers watering
	NextWater     CareTask `json:"nextWater"`
	NextFertilize CareTask `json:"nextFertilize"`
}

// Tunables of the water balance model. Kept as named constants so the ADR
// and the tests can reference them.
const (
	DefaultET0MM        = 3.0  // used when no weather is available for a day
	IndoorET0MM         = 1.2  // indoor evaporative demand is low and stable
	RainInterceptionMM  = 2.0  // light rain wets leaves and evaporates
	RainEfficiencyPot   = 0.5  // a pot catches far less rain than open soil
	RainEfficiencyGrnd  = 0.8  // effective rainfall in ground
	HeatStressTempC     = 32.0 // above this, outdoor demand rises
	HeatStressFactor    = 1.25
	PotThresholdFactor  = 0.6 // a pot holds less water than the open ground
	HeavyRainMM         = 10.0
	maxFertilizeShifts  = 3
	extrapolationWindow = 3
)

// weatherIndex is a lookup of weather by day.
type weatherIndex map[time.Time]WeatherDay

func indexWeather(days []WeatherDay) weatherIndex {
	idx := make(weatherIndex, len(days))
	for _, d := range days {
		idx[Day(d.Date)] = d
	}
	return idx
}

// potFactor: smaller pots dry faster. 30 cm or more behaves like 1.3x ground,
// a 10 cm pot loses about 1.63x as much water as the same plant in ground.
func potFactor(p Plant) float64 {
	if p.Placement != Pot {
		return 1.0
	}
	d := p.PotDiameterCM
	if d <= 0 {
		d = 20
	}
	return 1.3 + math.Max(0, float64(30-d))/30*0.5
}

// DailyLoss is the water the plant's root zone loses on a given day (mm).
func DailyLoss(p Plant, prof CareProfile, w WeatherDay, known bool) float64 {
	et0 := DefaultET0MM
	if p.Exposure == Indoor {
		et0 = IndoorET0MM
	} else if known {
		et0 = w.ET0MM
	}
	loss := et0 * prof.CropCoefficient * potFactor(p)
	if p.Exposure == Outdoor && known && w.TMaxC >= HeatStressTempC {
		loss *= HeatStressFactor
	}
	return loss
}

// EffectiveRain is how much of the day's rain actually reaches the roots.
func EffectiveRain(p Plant, w WeatherDay, known bool) float64 {
	if p.Exposure == Indoor || !known {
		return 0
	}
	eff := RainEfficiencyGrnd
	if p.Placement == Pot {
		eff = RainEfficiencyPot
	}
	return math.Max(0, w.PrecipMM-RainInterceptionMM) * eff
}

// Threshold is the deficit (mm) at which the plant needs water.
func Threshold(p Plant, prof CareProfile) float64 {
	if p.Placement == Pot {
		return prof.AllowableDepletion * PotThresholdFactor
	}
	return prof.AllowableDepletion
}

// PlanCare computes the next watering and fertilizing dates for a plant.
// It is a pure function: same inputs, same plan.
func PlanCare(p Plant, prof CareProfile, weather []WeatherDay, now time.Time) CarePlan {
	today := Day(now)
	idx := indexWeather(weather)
	plan := CarePlan{PlantID: p.ID, ThresholdMM: round1(Threshold(p, prof))}
	plan.NextWater, plan.DeficitMM = planWater(p, prof, idx, today)
	plan.NextFertilize = planFertilize(p, prof, idx, today)
	return plan
}

func planWater(p Plant, prof CareProfile, idx weatherIndex, today time.Time) (CareTask, float64) {
	task := CareTask{PlantID: p.ID, Kind: Water}
	last := Day(p.LastWatered)
	threshold := Threshold(p, prof)
	earliest := last.AddDate(0, 0, prof.MinIntervalDays)
	latest := last.AddDate(0, 0, prof.MaxIntervalDays)

	// Horizon: simulate through the last day we have weather for, or today.
	horizon := today
	for d := range idx {
		if d.After(horizon) {
			horizon = d
		}
	}

	var deficit, deficitToday float64
	var recentLoss []float64
	for d := last.AddDate(0, 0, 1); !d.After(horizon); d = d.AddDate(0, 0, 1) {
		w, known := idx[d]
		loss := DailyLoss(p, prof, w, known)
		recentLoss = append(recentLoss, loss)
		deficit = math.Max(0, deficit+loss-EffectiveRain(p, w, known))
		if !d.After(today) {
			deficitToday = deficit
		}
		if d.Before(earliest) {
			continue
		}
		if deficit >= threshold {
			// Rain-skip: if tomorrow's forecast rain will cover the deficit,
			// don't tell an outdoor gardener to water today.
			next, ok := idx[d.AddDate(0, 0, 1)]
			if ok && !d.Before(today) && EffectiveRain(p, next, true) >= deficit {
				continue
			}
			return finishWater(task, d, today, fmt.Sprintf(
				"soil deficit %.1f mm reached the %.1f mm threshold for %s",
				deficit, threshold, prof.CommonName)), round1(deficitToday)
		}
		if !d.Before(latest) {
			return finishWater(task, d, today, fmt.Sprintf(
				"maximum interval of %d days for %s", prof.MaxIntervalDays, prof.CommonName)), round1(deficitToday)
		}
	}

	// Beyond the forecast: extrapolate with the average of recent losses.
	avg := DefaultET0MM * prof.CropCoefficient * potFactor(p)
	if n := len(recentLoss); n > 0 {
		k := min(n, extrapolationWindow)
		var s float64
		for _, l := range recentLoss[n-k:] {
			s += l
		}
		avg = s / float64(k)
	}
	days := int(math.Ceil((threshold - deficit) / avg))
	due := horizon.AddDate(0, 0, max(1, days))
	if due.Before(earliest) {
		due = earliest
	}
	if due.After(latest) {
		due = latest
	}
	return finishWater(task, due, today, fmt.Sprintf(
		"projected from an average loss of %.1f mm/day beyond the forecast", avg)), round1(deficitToday)
}

func finishWater(t CareTask, due, today time.Time, reason string) CareTask {
	if due.Before(today) {
		t.Overdue = true
		reason = fmt.Sprintf("overdue since %s: %s", due.Format("Jan 2"), reason)
		due = today
	}
	t.Due, t.Reason = due, reason
	return t
}

// GrowingSeason reports whether plants are actively growing on that date.
// Northern hemisphere: March to September. Southern: September to March.
func GrowingSeason(d time.Time, southern bool) bool {
	m := d.Month()
	north := m >= time.March && m <= time.September
	if southern {
		return m >= time.September || m <= time.March
	}
	return north
}

func nextGrowingSeasonStart(d time.Time, southern bool) time.Time {
	start := time.March
	if southern {
		start = time.September
	}
	c := time.Date(d.Year(), start, 1, 0, 0, 0, 0, time.UTC)
	if !c.After(d) {
		c = c.AddDate(1, 0, 0)
	}
	return c
}

func planFertilize(p Plant, prof CareProfile, idx weatherIndex, today time.Time) CareTask {
	task := CareTask{PlantID: p.ID, Kind: Fertilize}
	southern := p.Location.SouthernHemisphere()
	var due time.Time
	reason := fmt.Sprintf("every %d days in the growing season", prof.FertilizeEveryDays)
	if p.LastFertilized.IsZero() {
		due, reason = today, "no feeding on record"
	} else {
		due = Day(p.LastFertilized).AddDate(0, 0, prof.FertilizeEveryDays)
	}
	if !GrowingSeason(maxTime(due, today), southern) {
		if prof.FeedWhenDormant {
			if !p.LastFertilized.IsZero() {
				due = Day(p.LastFertilized).AddDate(0, 0, prof.FertilizeEveryDays*2)
			}
			reason = "dormant season: feeding at half frequency"
		} else {
			due = nextGrowingSeasonStart(maxTime(due, today), southern)
			reason = "dormant season: resume feeding when growth restarts"
		}
	}
	if due.Before(today) {
		task.Overdue = true
		due = today
	}
	// Heavy rain right after feeding leaches nutrients out of the root zone.
	if p.Exposure == Outdoor {
		for i := 0; i < maxFertilizeShifts; i++ {
			w, ok := idx[due]
			if !ok || w.PrecipMM < HeavyRainMM {
				break
			}
			due = due.AddDate(0, 0, 1)
			reason = fmt.Sprintf("postponed: %.0f mm of rain would wash the fertilizer away", w.PrecipMM)
		}
	}
	task.Due, task.Reason = due, reason
	return task
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
