export type Placement = 'pot' | 'ground'
export type Exposure = 'indoor' | 'outdoor'
export type TaskKind = 'water' | 'fertilize'

export interface Location { label: string; latitude: number; longitude: number }

export interface CareTask { plantId: string; kind: TaskKind; due: string; overdue: boolean; reason: string }

export interface Profile { species: string; commonName: string; notes: string; minIntervalDays: number; maxIntervalDays: number }

export interface Plant {
  id: string
  nickname: string
  species: string
  placement: Placement
  potDiameterCm?: number
  exposure: Exposure
  location: Location
  ownerEmail: string
  lastWatered: string
  lastFertilized?: string
  plan?: {
    deficitMm: number
    thresholdMm: number
    nextWater: CareTask
    nextFertilize: CareTask
    profile: Profile
    weatherDegraded: boolean
  }
}

export interface CalendarEntry extends CareTask { plantName: string; species: string }

export interface Finding { condition: string; label: string; confidence: number; evidence: string }
export interface Diagnosis {
  id: string
  plantId: string
  photoUrl: string
  provider: string
  createdAt: string
  summary: string
  findings: Finding[]
  actions: { priority: 'now' | 'soon' | 'monitor'; description: string }[]
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init)
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error ?? `HTTP ${res.status}`)
  }
  return res.status === 204 ? (undefined as T) : res.json()
}

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
})

export const api = {
  plants: () => req<Plant[]>('/api/plants'),
  species: () => req<Profile[]>('/api/species'),
  createPlant: (p: Omit<Plant, 'id' | 'plan'>) => req<Plant>('/api/plants', json('POST', p)),
  deletePlant: (id: string) => req<void>(`/api/plants/${id}`, { method: 'DELETE' }),
  care: (id: string, kind: TaskKind) => req<Plant>(`/api/plants/${id}/care`, json('POST', { kind })),
  calendar: (days = 14) => req<CalendarEntry[]>(`/api/calendar?days=${days}`),
  history: () => req<Diagnosis[]>('/api/diagnoses?limit=50'),
  diagnose: (id: string, file: Blob) => {
    const fd = new FormData()
    fd.append('photo', file)
    return req<Diagnosis>(`/api/plants/${id}/diagnoses`, { method: 'POST', body: fd })
  },
}
