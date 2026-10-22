// Package memory provides in-memory adapters for tests and single-node demos.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/ipenas-cl/leafwise/internal/domain"
)

// Store implements PlantRepository, DiagnosisRepository and ReminderLog.
type Store struct {
	mu        sync.RWMutex
	plants    map[string]domain.Plant
	diagnoses []domain.Diagnosis
	sent      map[string]bool
}

func NewStore() *Store {
	return &Store{plants: map[string]domain.Plant{}, sent: map[string]bool{}}
}

func (s *Store) Save(_ context.Context, p domain.Plant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plants[p.ID] = p
	return nil
}

func (s *Store) Get(_ context.Context, id string) (domain.Plant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plants[id]
	if !ok {
		return p, domain.ErrNotFound
	}
	return p, nil
}

func (s *Store) List(_ context.Context) ([]domain.Plant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Plant, 0, len(s.plants))
	for _, p := range s.plants {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID)
	})
	return out, nil
}

func (s *Store) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plants[id]; !ok {
		return domain.ErrNotFound
	}
	delete(s.plants, id)
	return nil
}

func (s *Store) SaveDiagnosis(_ context.Context, d domain.Diagnosis) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.diagnoses = append(s.diagnoses, d)
	return nil
}

func (s *Store) ListDiagnoses(_ context.Context, plantID string, limit int) ([]domain.Diagnosis, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Diagnosis
	for i := len(s.diagnoses) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if plantID == "" || s.diagnoses[i].PlantID == plantID {
			out = append(out, s.diagnoses[i])
		}
	}
	return out, nil
}

func (s *Store) MarkSent(_ context.Context, plantID string, kind domain.TaskKind, due time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := fmt.Sprintf("%s|%s|%s", plantID, kind, due.Format("2006-01-02"))
	if s.sent[k] {
		return false, nil
	}
	s.sent[k] = true
	return true, nil
}

// Photos is an in-memory PhotoStore.
type Photos struct {
	mu   sync.RWMutex
	objs map[string][2][]byte
}

func NewPhotos() *Photos { return &Photos{objs: map[string][2][]byte{}} }

func (p *Photos) Put(_ context.Context, key, ct string, r io.Reader, _ int64) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.objs[key] = [2][]byte{b, []byte(ct)}
	return nil
}

func (p *Photos) Get(_ context.Context, key string) (io.ReadCloser, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	o, ok := p.objs[key]
	if !ok {
		return nil, "", domain.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(o[0])), string(o[1]), nil
}

// Locker is a process-local lock.
type Locker struct{ mu sync.Mutex }

func (l *Locker) TryLock(context.Context, string) (func(), bool, error) {
	if !l.mu.TryLock() {
		return nil, false, nil
	}
	return l.mu.Unlock, true, nil
}
