import { useCallback, useEffect, useState } from 'react'
import { api, type CalendarEntry, type Diagnosis, type Plant, type Profile } from './api'
import { conditionLabel, dryness, groupByDay, percent, relativeDay } from './format'

type Tab = 'plants' | 'calendar' | 'diagnose' | 'history'
const tabs: { id: Tab; label: string; icon: string }[] = [
  { id: 'plants', label: 'Plants', icon: '🪴' },
  { id: 'calendar', label: 'Calendar', icon: '📅' },
  { id: 'diagnose', label: 'Diagnose', icon: '📷' },
  { id: 'history', label: 'History', icon: '🩺' },
]

function useHashTab(): [Tab, (t: Tab) => void] {
  const read = () => ((location.hash.replace('#/', '') || 'plants') as Tab)
  const [tab, setTab] = useState<Tab>(read)
  useEffect(() => {
    const on = () => setTab(read())
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  return [tab, (t) => (location.hash = `/${t}`)]
}

export default function App() {
  const [tab, go] = useHashTab()
  const [plants, setPlants] = useState<Plant[]>([])
  const [error, setError] = useState('')
  const refresh = useCallback(() => api.plants().then(setPlants).catch((e) => setError(e.message)), [])
  useEffect(() => void refresh(), [refresh])

  return (
    <div className="app">
      <header>
        <h1>🌿 Leafwise</h1>
        <span className="sub">Weather-aware plant care</span>
      </header>
      {error && <div className="error" onClick={() => setError('')}>{error}</div>}
      <main>
        {tab === 'plants' && <Plants plants={plants} refresh={refresh} onError={setError} />}
        {tab === 'calendar' && <Calendar />}
        {tab === 'diagnose' && <Diagnose plants={plants} onDone={() => go('history')} onError={setError} />}
        {tab === 'history' && <History plants={plants} />}
      </main>
      <nav>
        {tabs.map((t) => (
          <button key={t.id} className={tab === t.id ? 'active' : ''} onClick={() => go(t.id)}>
            <span>{t.icon}</span>
            {t.label}
          </button>
        ))}
      </nav>
    </div>
  )
}

function Plants({ plants, refresh, onError }: { plants: Plant[]; refresh: () => void; onError: (m: string) => void }) {
  const [adding, setAdding] = useState(false)
  const care = (id: string, kind: 'water' | 'fertilize') => api.care(id, kind).then(refresh).catch((e) => onError(e.message))
  return (
    <section>
      <div className="row">
        <h2>My plants</h2>
        <button className="primary" onClick={() => setAdding(!adding)}>{adding ? 'Close' : '+ Add plant'}</button>
      </div>
      {adding && <AddPlant onAdded={() => { setAdding(false); refresh() }} onError={onError} />}
      {plants.length === 0 && <p className="muted">No plants yet. Add your first one.</p>}
      <div className="cards">
        {plants.map((p) => {
          const plan = p.plan!
          const dry = dryness(plan.deficitMm, plan.thresholdMm)
          return (
            <article key={p.id} className="card">
              <div className="row">
                <div>
                  <h3>{p.nickname}</h3>
                  <div className="muted">
                    {plan.profile.commonName} · {p.exposure} · {p.placement}
                    {p.placement === 'pot' && p.potDiameterCm ? ` ${p.potDiameterCm} cm` : ''}
                  </div>
                </div>
                <span className="badge">{p.location.label || 'No location'}</span>
              </div>
              <div className="meter" title={`Soil deficit ${plan.deficitMm} / ${plan.thresholdMm} mm`}>
                <div style={{ width: percent(dry), background: dry > 0.85 ? '#c0582b' : dry > 0.6 ? '#d9a521' : '#3d8b4d' }} />
              </div>
              <div className="tasks">
                <Task icon="💧" label="Water" due={plan.nextWater.due} overdue={plan.nextWater.overdue} reason={plan.nextWater.reason} onDone={() => care(p.id, 'water')} />
                <Task icon="🌱" label="Feed" due={plan.nextFertilize.due} overdue={plan.nextFertilize.overdue} reason={plan.nextFertilize.reason} onDone={() => care(p.id, 'fertilize')} />
              </div>
              {plan.weatherDegraded && <p className="warn">Weather service unavailable, using seasonal defaults.</p>}
            </article>
          )
        })}
      </div>
    </section>
  )
}

function Task(props: { icon: string; label: string; due: string; overdue: boolean; reason: string; onDone: () => void }) {
  const when = relativeDay(props.due)
  const urgent = props.overdue || when === 'today'
  return (
    <div className={`task ${urgent ? 'urgent' : ''}`}>
      <div>
        <strong>{props.icon} {props.label} {when}</strong>
        <div className="reason">{props.reason}</div>
      </div>
      <button onClick={props.onDone}>Done</button>
    </div>
  )
}

function AddPlant({ onAdded, onError }: { onAdded: () => void; onError: (m: string) => void }) {
  const [species, setSpecies] = useState<Profile[]>([])
  const [form, setForm] = useState({
    nickname: '', species: 'ocimum-basilicum', placement: 'pot' as const, potDiameterCm: 20, exposure: 'outdoor' as const,
    label: 'Balcony', latitude: -33.45, longitude: -70.66, ownerEmail: 'gardener@example.com',
  })
  useEffect(() => void api.species().then(setSpecies), [])
  const set = (k: string, v: string | number) => setForm({ ...form, [k]: v })
  const locate = () =>
    navigator.geolocation?.getCurrentPosition((pos) =>
      setForm({ ...form, latitude: +pos.coords.latitude.toFixed(3), longitude: +pos.coords.longitude.toFixed(3) }))
  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    api.createPlant({
      nickname: form.nickname, species: form.species, placement: form.placement, potDiameterCm: form.potDiameterCm,
      exposure: form.exposure, ownerEmail: form.ownerEmail, lastWatered: new Date().toISOString(),
      location: { label: form.label, latitude: form.latitude, longitude: form.longitude },
    }).then(onAdded).catch((err) => onError(err.message))
  }
  return (
    <form className="card form" onSubmit={submit}>
      <label>Name<input required value={form.nickname} onChange={(e) => set('nickname', e.target.value)} placeholder="e.g. Kitchen basil" /></label>
      <label>Species
        <select value={form.species} onChange={(e) => set('species', e.target.value)}>
          {species.map((s) => <option key={s.species} value={s.species}>{s.commonName}</option>)}
        </select>
      </label>
      <div className="grid2">
        <label>Placement
          <select value={form.placement} onChange={(e) => set('placement', e.target.value)}>
            <option value="pot">Pot</option><option value="ground">Ground</option>
          </select>
        </label>
        <label>Exposure
          <select value={form.exposure} onChange={(e) => set('exposure', e.target.value)}>
            <option value="outdoor">Outdoor</option><option value="indoor">Indoor</option>
          </select>
        </label>
      </div>
      {form.placement === 'pot' && <label>Pot diameter (cm)<input type="number" min={5} max={200} value={form.potDiameterCm} onChange={(e) => set('potDiameterCm', +e.target.value)} /></label>}
      <label>Location<input value={form.label} onChange={(e) => set('label', e.target.value)} /></label>
      <div className="grid2">
        <label>Lat<input type="number" step="0.001" value={form.latitude} onChange={(e) => set('latitude', +e.target.value)} /></label>
        <label>Lon<input type="number" step="0.001" value={form.longitude} onChange={(e) => set('longitude', +e.target.value)} /></label>
      </div>
      <button type="button" onClick={locate}>📍 Use my location</button>
      <label>Reminder email<input type="email" value={form.ownerEmail} onChange={(e) => set('ownerEmail', e.target.value)} /></label>
      <button className="primary" type="submit">Save plant</button>
    </form>
  )
}

function Calendar() {
  const [entries, setEntries] = useState<CalendarEntry[]>([])
  useEffect(() => void api.calendar(14).then(setEntries), [])
  return (
    <section>
      <h2>Next 14 days</h2>
      {groupByDay(entries).map(([day, items]) => (
        <div key={day} className="day">
          <h4>
            {new Date(day + 'T12:00:00Z').toLocaleDateString('en', { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' })}
            <span className="muted"> · {relativeDay(day + 'T00:00:00Z')}</span>
          </h4>
          {items.map((e, i) => (
            <div key={i} className={`entry ${e.kind}`}>
              <span>{e.kind === 'water' ? '💧' : '🌱'}</span>
              <div>
                <strong>{e.kind === 'water' ? 'Water' : 'Feed'} {e.plantName}</strong>
                <div className="reason">{e.reason}</div>
              </div>
            </div>
          ))}
        </div>
      ))}
    </section>
  )
}

function Diagnose({ plants, onDone, onError }: { plants: Plant[]; onDone: () => void; onError: (m: string) => void }) {
  const [plantId, setPlantId] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<Diagnosis | null>(null)
  const preview = file ? URL.createObjectURL(file) : ''
  const id = plantId || plants[0]?.id
  const run = () => {
    if (!file || !id) return
    setBusy(true)
    api.diagnose(id, file).then(setResult).catch((e) => onError(e.message)).finally(() => setBusy(false))
  }
  return (
    <section>
      <h2>Photo check-up</h2>
      <div className="card form">
        <label>Plant
          <select value={id} onChange={(e) => setPlantId(e.target.value)}>
            {plants.map((p) => <option key={p.id} value={p.id}>{p.nickname}</option>)}
          </select>
        </label>
        <label className="upload">
          {preview ? <img src={preview} alt="preview" /> : <span>📷 Take or choose a photo</span>}
          <input type="file" accept="image/*" capture="environment" onChange={(e) => { setFile(e.target.files?.[0] ?? null); setResult(null) }} />
        </label>
        <button className="primary" disabled={!file || busy} onClick={run}>{busy ? 'Analysing…' : 'Diagnose'}</button>
      </div>
      {result && <DiagnosisCard d={result} />}
      {result && <button onClick={onDone}>See history</button>}
    </section>
  )
}

function DiagnosisCard({ d, plantName }: { d: Diagnosis; plantName?: string }) {
  const top = [...d.findings].sort((a, b) => b.confidence - a.confidence)[0]
  return (
    <article className={`card diag ${top.condition}`}>
      <div className="diag-head">
        <img src={d.photoUrl} alt="plant" />
        <div>
          {plantName && <div className="muted">{plantName} · {new Date(d.createdAt).toLocaleDateString()}</div>}
          <h3>{top.label}</h3>
          <span className={`pill ${top.condition}`}>{conditionLabel[top.condition]} · {percent(top.confidence)}</span>
        </div>
      </div>
      <p>{d.summary}</p>
      {d.findings.length > 1 && (
        <ul className="findings">
          {d.findings.map((f, i) => <li key={i}>{f.label} <span className="muted">({percent(f.confidence)})</span></li>)}
        </ul>
      )}
      <ul className="actions">
        {d.actions.map((a, i) => <li key={i}><span className={`prio ${a.priority}`}>{a.priority}</span>{a.description}</li>)}
      </ul>
      <div className="muted small">via {d.provider}</div>
    </article>
  )
}

function History({ plants }: { plants: Plant[] }) {
  const [items, setItems] = useState<Diagnosis[]>([])
  useEffect(() => void api.history().then(setItems), [])
  const name = (id: string) => plants.find((p) => p.id === id)?.nickname
  return (
    <section>
      <h2>Diagnosis history</h2>
      {items.length === 0 && <p className="muted">No photos yet.</p>}
      <div className="cards">{items.map((d) => <DiagnosisCard key={d.id} d={d} plantName={name(d.plantId)} />)}</div>
    </section>
  )
}
