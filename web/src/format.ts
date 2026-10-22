import type { CalendarEntry } from './api'

const DAY = 86_400_000

/** Whole days between two dates, ignoring time of day (UTC calendar days). */
export function daysBetween(from: Date, to: Date): number {
  const a = Date.UTC(from.getUTCFullYear(), from.getUTCMonth(), from.getUTCDate())
  const b = Date.UTC(to.getUTCFullYear(), to.getUTCMonth(), to.getUTCDate())
  return Math.round((b - a) / DAY)
}

/** "today", "tomorrow", "in 3 days", "2 days ago". */
export function relativeDay(iso: string, now = new Date()): string {
  const d = daysBetween(now, new Date(iso))
  if (d === 0) return 'today'
  if (d === 1) return 'tomorrow'
  if (d === -1) return 'yesterday'
  return d > 0 ? `in ${d} days` : `${-d} days ago`
}

/** Groups calendar entries by ISO date, preserving order. */
export function groupByDay(entries: CalendarEntry[]): [string, CalendarEntry[]][] {
  const map = new Map<string, CalendarEntry[]>()
  for (const e of entries) {
    const k = e.due.slice(0, 10)
    map.set(k, [...(map.get(k) ?? []), e])
  }
  return [...map.entries()]
}

export function percent(x: number): string {
  return `${Math.round(x * 100)}%`
}

/** 0..1 how close the soil is to needing water. */
export function dryness(deficit: number, threshold: number): number {
  if (threshold <= 0) return 0
  return Math.max(0, Math.min(1, deficit / threshold))
}

export const conditionLabel: Record<string, string> = {
  healthy: 'Healthy',
  pest: 'Pest',
  disease: 'Disease',
  overwatering: 'Overwatering',
  underwatering: 'Underwatering',
  nutrient_deficiency: 'Nutrient deficiency',
}
