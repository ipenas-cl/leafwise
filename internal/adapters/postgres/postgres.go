// Package postgres implements the repository ports on PostgreSQL via pgx.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ipenas-cl/leafwise/internal/domain"
	"github.com/ipenas-cl/leafwise/migrations"
)

type Store struct{ DB *pgxpool.Pool }

// Open connects and applies pending migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	// Retry while the database container is starting.
	for i := 0; ; i++ {
		if err = pool.Ping(ctx); err == nil || i == 30 {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	s := &Store{DB: pool}
	return s, s.migrate(ctx)
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.DB.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, n := range names {
		var done bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)`, n).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, _ := migrations.FS.ReadFile(n)
		err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations(name) VALUES ($1)`, n)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", n, err)
		}
	}
	return nil
}

const plantCols = `id, nickname, species, placement, pot_diameter_cm, exposure, location_label, latitude, longitude, owner_email, last_watered, last_fertilized, created_at`

func (s *Store) Save(ctx context.Context, p domain.Plant) error {
	var lf *time.Time
	if !p.LastFertilized.IsZero() {
		lf = &p.LastFertilized
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO plants (`+plantCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (id) DO UPDATE SET nickname=$2, species=$3, placement=$4, pot_diameter_cm=$5, exposure=$6,
 location_label=$7, latitude=$8, longitude=$9, owner_email=$10, last_watered=$11, last_fertilized=$12`,
		p.ID, p.Nickname, p.Species, p.Placement, p.PotDiameterCM, p.Exposure, p.Location.Label,
		p.Location.Latitude, p.Location.Longitude, p.OwnerEmail, p.LastWatered, lf, p.CreatedAt)
	return err
}

func scanPlant(row pgx.Row) (domain.Plant, error) {
	var p domain.Plant
	var lf *time.Time
	err := row.Scan(&p.ID, &p.Nickname, &p.Species, &p.Placement, &p.PotDiameterCM, &p.Exposure,
		&p.Location.Label, &p.Location.Latitude, &p.Location.Longitude, &p.OwnerEmail, &p.LastWatered, &lf, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, domain.ErrNotFound
	}
	if lf != nil {
		p.LastFertilized = *lf
	}
	return p, err
}

func (s *Store) Get(ctx context.Context, id string) (domain.Plant, error) {
	return scanPlant(s.DB.QueryRow(ctx, `SELECT `+plantCols+` FROM plants WHERE id=$1`, id))
}

func (s *Store) List(ctx context.Context) ([]domain.Plant, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+plantCols+` FROM plants ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Plant
	for rows.Next() {
		p, err := scanPlant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Delete(ctx context.Context, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM plants WHERE id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (s *Store) SaveDiagnosis(ctx context.Context, d domain.Diagnosis) error {
	a, _ := json.Marshal(d.Assessment)
	_, err := s.DB.Exec(ctx, `INSERT INTO diagnoses (id, plant_id, photo_key, provider, assessment, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		d.ID, d.PlantID, d.PhotoKey, d.Provider, a, d.CreatedAt)
	return err
}

func (s *Store) ListDiagnoses(ctx context.Context, plantID string, limit int) ([]domain.Diagnosis, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `SELECT id, plant_id, photo_key, provider, assessment, created_at FROM diagnoses
WHERE ($1 = '' OR plant_id = $1) ORDER BY created_at DESC LIMIT $2`, plantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Diagnosis
	for rows.Next() {
		var d domain.Diagnosis
		var a []byte
		if err := rows.Scan(&d.ID, &d.PlantID, &d.PhotoKey, &d.Provider, &a, &d.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(a, &d.Assessment); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) MarkSent(ctx context.Context, plantID string, kind domain.TaskKind, due time.Time) (bool, error) {
	tag, err := s.DB.Exec(ctx, `INSERT INTO reminders_sent (plant_id, kind, due_date) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
		plantID, string(kind), domain.Day(due))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// TryLock takes a session-level advisory lock so only one replica runs a job.
func (s *Store) TryLock(ctx context.Context, name string) (func(), bool, error) {
	h := fnv.New64a()
	h.Write([]byte(name))
	key := int64(h.Sum64())
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok); err != nil || !ok {
		conn.Release()
		return nil, false, err
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
		conn.Release()
	}, true, nil
}

// Ping is used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error { return s.DB.Ping(ctx) }
