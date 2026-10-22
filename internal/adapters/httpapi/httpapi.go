// Package httpapi is the driving adapter: a JSON REST API over net/http.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/ipenas-cl/leafwise/internal/adapters/telemetry"
	"github.com/ipenas-cl/leafwise/internal/app"
	"github.com/ipenas-cl/leafwise/internal/domain"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

type Server struct {
	Plants    *app.PlantService
	Diagnosis *app.DiagnosisService
	Reminders *app.ReminderJob
	Photos    ports.PhotoStore
	Log       *slog.Logger
	Ready     func(context.Context) error
	Config    map[string]any
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", s.ready)
	mux.Handle("GET /metrics", promhttp.Handler())

	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, s.Config) })
	mux.HandleFunc("GET /api/species", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, domain.Profiles()) })
	mux.HandleFunc("GET /api/plants", s.listPlants)
	mux.HandleFunc("POST /api/plants", s.createPlant)
	mux.HandleFunc("GET /api/plants/{id}", s.getPlant)
	mux.HandleFunc("DELETE /api/plants/{id}", s.deletePlant)
	mux.HandleFunc("POST /api/plants/{id}/care", s.recordCare)
	mux.HandleFunc("POST /api/plants/{id}/diagnoses", s.diagnose)
	mux.HandleFunc("GET /api/plants/{id}/diagnoses", s.history)
	mux.HandleFunc("GET /api/diagnoses", s.history)
	mux.HandleFunc("GET /api/calendar", s.calendar)
	mux.HandleFunc("GET /api/photos/{key...}", s.photo)
	mux.HandleFunc("POST /api/reminders/run", s.runReminders)

	return otelhttp.NewHandler(telemetry.Metrics(mux), "leafwise",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return r.Method + " " + r.URL.Path }))
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.Ready != nil {
		if err := s.Ready(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	w.Write([]byte("ready"))
}

type plantView struct {
	domain.Plant
	Plan app.PlanView `json:"plan"`
}

func (s *Server) listPlants(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Plants.List(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]plantView, 0, len(ps))
	for _, p := range ps {
		v, err := s.Plants.Plan(r.Context(), p)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v.Weather = nil // keep the list light
		out = append(out, plantView{p, v})
	}
	writeJSON(w, 200, out)
}

func (s *Server) createPlant(w http.ResponseWriter, r *http.Request) {
	var p domain.Plant
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&p); err != nil {
		writeJSON(w, 400, errBody("invalid JSON body"))
		return
	}
	p.ID = ""
	p, err := s.Plants.Register(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) getPlant(w http.ResponseWriter, r *http.Request) {
	p, err := s.Plants.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.Plants.Plan(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, plantView{p, v})
}

func (s *Server) deletePlant(w http.ResponseWriter, r *http.Request) {
	if err := s.Plants.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) recordCare(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind domain.TaskKind `json:"kind"`
		At   time.Time       `json:"at"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		writeJSON(w, 400, errBody("invalid JSON body"))
		return
	}
	p, err := s.Plants.RecordCare(r.Context(), r.PathValue("id"), body.Kind, body.At)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) diagnose(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, app.MaxPhotoBytes+1<<20)
	f, _, err := r.FormFile("photo")
	if err != nil {
		writeJSON(w, 400, errBody("multipart field 'photo' is required"))
		return
	}
	defer f.Close()
	img, err := io.ReadAll(f)
	if err != nil {
		writeJSON(w, 400, errBody("could not read photo"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	d, err := s.Diagnosis.Diagnose(ctx, r.PathValue("id"), img)
	if err != nil {
		reason := "internal"
		switch {
		case errors.Is(err, app.ErrInvalidAssessment):
			reason = "schema"
		case errors.Is(err, domain.ErrValidation):
			reason = "input"
		}
		telemetry.DiagnosisErrors.WithLabelValues(reason).Inc()
		s.fail(w, r, err)
		return
	}
	telemetry.Diagnoses.WithLabelValues(d.Provider, string(d.Primary().Condition)).Inc()
	d.PhotoURL = "/api/photos/" + d.PhotoKey
	writeJSON(w, 201, d)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ds, err := s.Diagnosis.History(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if ds == nil {
		ds = []domain.Diagnosis{}
	}
	for i := range ds {
		ds[i].PhotoURL = "/api/photos/" + ds[i].PhotoKey
	}
	writeJSON(w, 200, ds)
}

func (s *Server) calendar(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 60 {
		days = 14
	}
	cal, err := s.Plants.Calendar(r.Context(), days)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if cal == nil {
		cal = []app.CalendarEntry{}
	}
	writeJSON(w, 200, cal)
}

func (s *Server) photo(w http.ResponseWriter, r *http.Request) {
	rc, ct, err := s.Photos.Get(r.Context(), r.PathValue("key"))
	if err != nil {
		s.fail(w, r, domain.ErrNotFound)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	_, _ = io.Copy(w, rc)
}

func (s *Server) runReminders(w http.ResponseWriter, r *http.Request) {
	n, err := s.Reminders.RunOnce(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	telemetry.RemindersSent.Add(float64(n))
	writeJSON(w, 200, map[string]int{"sent": n})
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeJSON(w, 404, errBody("not found"))
	case errors.Is(err, domain.ErrValidation):
		writeJSON(w, 400, errBody(err.Error()))
	case errors.Is(err, app.ErrInvalidAssessment):
		writeJSON(w, 502, errBody("the diagnosis provider returned an invalid response"))
	default:
		s.Log.ErrorContext(r.Context(), "request failed", "path", r.URL.Path, "err", err)
		writeJSON(w, 500, errBody("internal error"))
	}
}

func errBody(msg string) map[string]string { return map[string]string{"error": msg} }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
