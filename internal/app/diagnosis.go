package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/ipenas-cl/leafwise/api"
	"github.com/ipenas-cl/leafwise/internal/domain"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

// MaxPhotoBytes caps uploads; vision models do not need more.
const MaxPhotoBytes = 8 << 20

// ErrInvalidAssessment means a diagnoser returned JSON that broke the contract.
var ErrInvalidAssessment = errors.New("diagnoser returned an invalid assessment")

// DiagnosisService runs the photo diagnosis use case.
type DiagnosisService struct {
	Plants    ports.PlantRepository
	Store     ports.DiagnosisRepository
	Photos    ports.PhotoStore
	Diagnoser ports.Diagnoser
	Notifier  ports.Notifier
	Clock     ports.Clock
	Log       *slog.Logger
	schema    *jsonschema.Schema
}

// CompileSchema loads the embedded JSON Schema. Call once at startup.
func CompileSchema() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(api.DiagnosisSchema))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	const url = "https://github.com/ipenas-cl/leafwise/api/diagnosis.schema.json"
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

// ParseAssessment validates raw adapter output against the schema and decodes it.
func ParseAssessment(schema *jsonschema.Schema, raw json.RawMessage) (domain.Assessment, error) {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return domain.Assessment{}, fmt.Errorf("%w: not JSON: %v", ErrInvalidAssessment, err)
	}
	if err := schema.Validate(inst); err != nil {
		return domain.Assessment{}, fmt.Errorf("%w: %v", ErrInvalidAssessment, err)
	}
	var a domain.Assessment
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, fmt.Errorf("%w: %v", ErrInvalidAssessment, err)
	}
	return a, nil
}

// Diagnose stores the photo, asks the diagnoser, validates and persists the result.
func (s *DiagnosisService) Diagnose(ctx context.Context, plantID string, image []byte) (domain.Diagnosis, error) {
	if s.schema == nil {
		sc, err := CompileSchema()
		if err != nil {
			return domain.Diagnosis{}, err
		}
		s.schema = sc
	}
	p, err := s.Plants.Get(ctx, plantID)
	if err != nil {
		return domain.Diagnosis{}, err
	}
	if len(image) == 0 || len(image) > MaxPhotoBytes {
		return domain.Diagnosis{}, fmt.Errorf("%w: photo must be between 1 byte and %d MB", domain.ErrValidation, MaxPhotoBytes>>20)
	}
	mime := http.DetectContentType(image)
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" {
		return domain.Diagnosis{}, fmt.Errorf("%w: unsupported image type %s", domain.ErrValidation, mime)
	}
	prof, _ := domain.LookupProfile(p.Species)
	id := NewID()
	now := s.Clock.Now().UTC()
	key := fmt.Sprintf("plants/%s/%s/%s.%s", p.ID, now.Format("2006/01/02"), id, strings.TrimPrefix(mime, "image/"))
	if err := s.Photos.Put(ctx, key, mime, bytes.NewReader(image), int64(len(image))); err != nil {
		return domain.Diagnosis{}, fmt.Errorf("store photo: %w", err)
	}

	raw, err := s.Diagnoser.Diagnose(ctx, ports.DiagnosisInput{
		Image: image, MIMEType: mime, Species: p.Species, CommonName: prof.CommonName,
		Placement: p.Placement, Exposure: p.Exposure,
	})
	if err != nil {
		return domain.Diagnosis{}, fmt.Errorf("diagnose: %w", err)
	}
	a, err := ParseAssessment(s.schema, raw)
	if err != nil {
		return domain.Diagnosis{}, err
	}
	d := domain.Diagnosis{ID: id, PlantID: p.ID, PhotoKey: key, Provider: s.Diagnoser.Name(), CreatedAt: now, Assessment: a}
	if err := s.Store.SaveDiagnosis(ctx, d); err != nil {
		return d, err
	}
	if a.NeedsAttention() && s.Notifier != nil {
		f := a.Primary()
		n := ports.Notification{To: p.OwnerEmail, PlantID: p.ID,
			Subject: fmt.Sprintf("%s needs attention: %s", p.Nickname, f.Label),
			Body:    fmt.Sprintf("%s\n\nConfidence: %.0f%%\n%s", a.Summary, f.Confidence*100, formatActions(a.Actions))}
		if err := s.Notifier.Notify(ctx, n); err != nil {
			s.Log.WarnContext(ctx, "diagnosis notification failed", "err", err)
		}
	}
	return d, nil
}

// History lists past diagnoses for a plant (or all plants when plantID is "").
func (s *DiagnosisService) History(ctx context.Context, plantID string, limit int) ([]domain.Diagnosis, error) {
	return s.Store.ListDiagnoses(ctx, plantID, limit)
}

func formatActions(as []domain.Action) string {
	var b strings.Builder
	for _, a := range as {
		fmt.Fprintf(&b, "- [%s] %s\n", a.Priority, a.Description)
	}
	return b.String()
}
