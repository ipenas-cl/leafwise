# ADR 0002: Schedule watering with a soil water balance, not fixed intervals

- Status: accepted
- Date: 2026-10-22

## Context

"Water every 3 days" is wrong half the time: a heat wave doubles demand, a
storm makes watering pointless, and a 12 cm pot dries far faster than a bed.

## Decision

Use a simplified FAO-56 daily water balance per plant:

- loss = ET0 x Kc x pot factor (x1.25 above 32 C outdoors). Indoor plants use a
  constant ET0 of 1.2 mm/day.
- effective rain = max(0, rain - 2 mm interception) x 0.8 in ground, x0.5 in pots;
  indoor plants get none.
- water when the accumulated deficit reaches the species' readily available
  water (x0.6 in pots), bounded by min/max interval guards.
- if tomorrow's forecast rain covers today's deficit, skip watering today.
- weather comes from Open-Meteo: 14 days of history + 7 days of forecast
  (`et0_fao_evapotranspiration`, `precipitation_sum`, temperatures). Beyond the
  forecast, extrapolate with the mean of the last 3 days.

Fertilizing follows the species interval, only in the growing season of the
plant's hemisphere (Sept to Mar in Chile), at half frequency for heavy feeders
in winter, and shifts away from days with 10+ mm of rain (leaching).

## Consequences

- Recommendations come with a human-readable reason ("soil deficit 15.2 mm
  reached the 15 mm threshold").
- `PlanCare` is pure and deterministic: it is the most heavily tested code.
- If Open-Meteo is down, plans degrade to climatology defaults and the UI shows
  a warning instead of failing (cache serves stale data first).
