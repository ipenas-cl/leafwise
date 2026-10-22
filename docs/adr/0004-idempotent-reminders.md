# ADR 0004: Idempotent reminders with a DB unique key and an advisory lock

- Status: accepted
- Date: 2026-10-22

## Context

The reminder job runs every few minutes, possibly on several replicas, and must
never spam the same "water your basil" email.

## Decision

- Every replica runs the scheduler loop, but each run first takes a Postgres
  session advisory lock (`pg_try_advisory_lock`); losers skip the run.
- Before sending, the job inserts `(plant_id, kind, due_date)` into
  `reminders_sent` with `ON CONFLICT DO NOTHING`; it only notifies when the row
  was inserted. This is safe even if the lock is lost mid-run.
- Notifications fan out to email (SMTP) and push (ntfy-compatible); one healthy
  channel counts as delivered.

## Consequences

At-most-once per task and due date. A crash between insert and send can drop a
reminder; acceptable for gardening, and the next due date produces a new one.
