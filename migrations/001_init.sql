CREATE TABLE IF NOT EXISTS plants (
    id               TEXT PRIMARY KEY,
    nickname         TEXT NOT NULL,
    species          TEXT NOT NULL,
    placement        TEXT NOT NULL CHECK (placement IN ('pot', 'ground')),
    pot_diameter_cm  INT  NOT NULL DEFAULT 0,
    exposure         TEXT NOT NULL CHECK (exposure IN ('indoor', 'outdoor')),
    location_label   TEXT NOT NULL DEFAULT '',
    latitude         DOUBLE PRECISION NOT NULL,
    longitude        DOUBLE PRECISION NOT NULL,
    owner_email      TEXT NOT NULL,
    last_watered     TIMESTAMPTZ NOT NULL,
    last_fertilized  TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS diagnoses (
    id          TEXT PRIMARY KEY,
    plant_id    TEXT NOT NULL REFERENCES plants(id) ON DELETE CASCADE,
    photo_key   TEXT NOT NULL,
    provider    TEXT NOT NULL,
    assessment  JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS diagnoses_plant_created ON diagnoses (plant_id, created_at DESC);

-- One row per reminder actually sent: the unique key makes the job idempotent.
CREATE TABLE IF NOT EXISTS reminders_sent (
    plant_id  TEXT NOT NULL REFERENCES plants(id) ON DELETE CASCADE,
    kind      TEXT NOT NULL,
    due_date  DATE NOT NULL,
    sent_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (plant_id, kind, due_date)
);
