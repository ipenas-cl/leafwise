# ADR 0001: Hexagonal architecture (ports and adapters)

- Status: accepted
- Date: 2026-10-22

## Context

Leafwise talks to a lot of outside systems: Postgres, S3-compatible storage, a
weather API, an ML inference service, vision LLMs, SMTP and push. Each has a
cheaper stand-in we want for tests and demos (memory store, static weather,
mock diagnoser, log notifier). The scheduling rules are the valuable part and
must be testable without any of them.

## Decision

- `internal/domain` holds entities and pure rules (`PlanCare`) with no I/O.
- `internal/ports` declares interfaces owned by the core: `PlantRepository`,
  `PhotoStore`, `WeatherProvider`, `Diagnoser`, `Notifier`, `Locker`, `Clock`.
- `internal/app` implements use cases against ports only.
- `internal/adapters/*` implement ports; `cmd/leafwise` is the only place that
  wires concrete adapters, driven by environment variables.

## Consequences

- Domain tests run in milliseconds with no containers.
- Swapping MinIO for S3 or R2, or OpenAI for Anthropic, is a config change.
- More files and interfaces than a "service + DB" layout. Worth it here given
  the number of integrations; it would be overkill for a CRUD app.
