package httpapi_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ipenas-cl/leafwise/internal/adapters/diagnosis"
	"github.com/ipenas-cl/leafwise/internal/adapters/httpapi"
	"github.com/ipenas-cl/leafwise/internal/adapters/memory"
	"github.com/ipenas-cl/leafwise/internal/adapters/notify"
	"github.com/ipenas-cl/leafwise/internal/adapters/openmeteo"
	"github.com/ipenas-cl/leafwise/internal/app"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

func server(t *testing.T) *httptest.Server {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := memory.NewStore()
	photos := memory.NewPhotos()
	clock := ports.SystemClock{}
	ps := &app.PlantService{Plants: store, Weather: openmeteo.Static{}, Clock: clock, Log: log}
	nt := notify.Log{L: log}
	s := &httpapi.Server{
		Plants:    ps,
		Diagnosis: &app.DiagnosisService{Plants: store, Store: store, Photos: photos, Diagnoser: diagnosis.Mock{}, Notifier: nt, Clock: clock, Log: log},
		Reminders: &app.ReminderJob{Plants: ps, Log: store, Notifier: nt, Clock: clock, Logger: log},
		Photos:    photos, Log: log, Config: map[string]any{"diagnosisProvider": "mock"},
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestEndToEnd(t *testing.T) {
	srv := server(t)
	body := `{"nickname":"Basil","species":"ocimum-basilicum","placement":"pot","potDiameterCm":14,
	"exposure":"outdoor","location":{"label":"Balcony","latitude":-33.45,"longitude":-70.66},
	"ownerEmail":"me@example.com","lastWatered":"2026-10-20T10:00:00Z"}`
	resp, err := http.Post(srv.URL+"/api/plants", "application/json", strings.NewReader(body))
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("create: %v %d", err, resp.StatusCode)
	}
	var p struct{ ID string }
	json.NewDecoder(resp.Body).Decode(&p)

	resp, _ = http.Get(srv.URL + "/api/plants/" + p.ID)
	var view struct {
		Plan struct {
			NextWater struct{ Reason string }
		}
	}
	json.NewDecoder(resp.Body).Decode(&view)
	if view.Plan.NextWater.Reason == "" {
		t.Fatal("plan should include a reason")
	}

	// Upload a photo for diagnosis.
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := 0; i < 256; i++ {
		img.Set(i%16, i/16, color.RGBA{40, 140, 50, 255})
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("photo", "leaf.png")
	png.Encode(fw, img)
	mw.Close()
	resp, _ = http.Post(srv.URL+"/api/plants/"+p.ID+"/diagnoses", mw.FormDataContentType(), &buf)
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("diagnose: %d %s", resp.StatusCode, b)
	}
	var d struct{ PhotoURL string }
	json.NewDecoder(resp.Body).Decode(&d)
	resp, _ = http.Get(srv.URL + d.PhotoURL)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("photo not served: %d", resp.StatusCode)
	}

	for _, path := range []string{"/api/calendar?days=14", "/api/diagnoses", "/api/species", "/metrics", "/healthz"} {
		if r, _ := http.Get(srv.URL + path); r.StatusCode != 200 {
			t.Errorf("%s: %d", path, r.StatusCode)
		}
	}
}

func TestErrorsMapToStatusCodes(t *testing.T) {
	srv := server(t)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/plants", `{"nickname":"x","species":"triffid"}`, 400},
		{"POST", "/api/plants", `not json`, 400},
		{"GET", "/api/plants/nope", "", 404},
		{"DELETE", "/api/plants/nope", "", 404},
		{"POST", "/api/plants/nope/care", `{"kind":"water"}`, 404},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, strings.NewReader(c.body))
		r, err := http.DefaultClient.Do(req)
		if err != nil || r.StatusCode != c.want {
			t.Errorf("%s %s: want %d got %d", c.method, c.path, c.want, r.StatusCode)
		}
	}
}
