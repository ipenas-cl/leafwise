// Command leafwise runs the Leafwise API and the reminder scheduler.
//
// Everything is configured with environment variables; with none set it runs
// fully in memory with mock diagnosis, which is handy for development.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ipenas-cl/leafwise/internal/adapters/diagnosis"
	"github.com/ipenas-cl/leafwise/internal/adapters/httpapi"
	"github.com/ipenas-cl/leafwise/internal/adapters/memory"
	"github.com/ipenas-cl/leafwise/internal/adapters/notify"
	"github.com/ipenas-cl/leafwise/internal/adapters/openmeteo"
	"github.com/ipenas-cl/leafwise/internal/adapters/postgres"
	"github.com/ipenas-cl/leafwise/internal/adapters/s3store"
	"github.com/ipenas-cl/leafwise/internal/adapters/telemetry"
	"github.com/ipenas-cl/leafwise/internal/app"
	"github.com/ipenas-cl/leafwise/internal/domain"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

var version = "dev"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

type repo interface {
	ports.PlantRepository
	ports.DiagnosisRepository
	ports.ReminderLog
	ports.Locker
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := telemetry.InitTracing(ctx, "leafwise-api", version, os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if err != nil {
		return err
	}
	defer shutdownTracing(context.Background())

	// --- Driven adapters -------------------------------------------------
	var store repo
	ready := func(context.Context) error { return nil }
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		pg, err := postgres.Open(ctx, dsn)
		if err != nil {
			return err
		}
		store, ready = pg, pg.Ping
		log.Info("using postgres")
	} else {
		mem := memory.NewStore()
		store = struct {
			*memory.Store
			*memory.Locker
		}{mem, &memory.Locker{}}
		log.Warn("DATABASE_URL not set, using in-memory storage")
	}

	var photos ports.PhotoStore = memory.NewPhotos()
	if ep := os.Getenv("S3_ENDPOINT"); ep != "" {
		s3, err := s3store.New(ctx, ep, os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"),
			env("S3_BUCKET", "leafwise-photos"), env("S3_REGION", "us-east-1"), env("S3_USE_SSL", "false") == "true")
		if err != nil {
			return err
		}
		photos = s3
		log.Info("using s3-compatible photo store", "endpoint", ep)
	}

	// Decorators: cache (serves stale on error) -> circuit breaker -> HTTP client with retries.
	var weather ports.WeatherProvider = &openmeteo.Cached{TTL: time.Hour, Next: &openmeteo.Breaker{
		Failures: 3, Cooldown: time.Minute, Next: &openmeteo.Client{BaseURL: os.Getenv("OPEN_METEO_URL")}}}
	if env("WEATHER_PROVIDER", "openmeteo") == "static" {
		weather = openmeteo.Static{}
	}

	diag, err := buildDiagnoser()
	if err != nil {
		return err
	}

	notifiers := notify.Fanout{notify.Log{L: log}}
	if addr := os.Getenv("SMTP_ADDR"); addr != "" {
		notifiers = append(notifiers, &notify.Email{Addr: addr, From: env("SMTP_FROM", "reminders@leafwise.local")})
	}
	if u := os.Getenv("PUSH_URL"); u != "" {
		notifiers = append(notifiers, &notify.Push{URL: u, Token: os.Getenv("PUSH_TOKEN")})
	}

	// --- Application core -----------------------------------------------
	clock := ports.SystemClock{}
	plants := &app.PlantService{Plants: store, Weather: weather, Clock: clock, Log: log}
	diagSvc := &app.DiagnosisService{Plants: store, Store: store, Photos: photos, Diagnoser: diag,
		Notifier: notifiers, Clock: clock, Log: log}
	reminders := &app.ReminderJob{Plants: plants, Log: store, Notifier: notifiers, Locker: store, Clock: clock, Logger: log,
		OnRun: func(n int, err error) {
			telemetry.RemindersSent.Add(float64(n))
			res := "ok"
			if err != nil {
				res = "error"
			}
			telemetry.ReminderRuns.WithLabelValues(res).Inc()
		}}

	if env("DEMO_SEED", "false") == "true" {
		if err := seed(ctx, plants); err != nil {
			return err
		}
	}

	if env("SCHEDULER_ENABLED", "true") == "true" {
		every, err := time.ParseDuration(env("REMINDER_INTERVAL", "15m"))
		if err != nil {
			return err
		}
		go reminders.Run(ctx, every)
	}

	// --- Driving adapter --------------------------------------------------
	srv := &httpapi.Server{Plants: plants, Diagnosis: diagSvc, Reminders: reminders, Photos: photos, Log: log, Ready: ready,
		Config: map[string]any{"version": version, "diagnosisProvider": diag.Name(), "notifiers": names(notifiers)}}
	hs := &http.Server{Addr: env("LISTEN_ADDR", ":8080"), Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	log.Info("listening", "addr", hs.Addr, "diagnosis", diag.Name())
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func names(f notify.Fanout) []string {
	var out []string
	for _, n := range f {
		out = append(out, n.Name())
	}
	return out
}

// seed registers a small demo garden in Santiago, Chile, once.
func seed(ctx context.Context, s *app.PlantService) error {
	existing, err := s.List(ctx)
	if err != nil || len(existing) > 0 {
		return err
	}
	now := time.Now()
	loc := domain.Location{Label: "Balcony, Santiago", Latitude: -33.45, Longitude: -70.66}
	garden := domain.Location{Label: "Backyard, Santiago", Latitude: -33.45, Longitude: -70.66}
	email := env("DEMO_OWNER_EMAIL", "gardener@example.com")
	ago := func(d int) time.Time { return now.AddDate(0, 0, -d) }
	demo := []domain.Plant{
		{Nickname: "Cherry tomatoes", Species: "solanum-lycopersicum", Placement: domain.Ground, Exposure: domain.Outdoor, Location: garden, LastWatered: ago(3), LastFertilized: ago(13)},
		{Nickname: "Basil on the sill", Species: "ocimum-basilicum", Placement: domain.Pot, PotDiameterCM: 14, Exposure: domain.Outdoor, Location: loc, LastWatered: ago(2), LastFertilized: ago(25)},
		{Nickname: "Lemon tree", Species: "citrus-limon", Placement: domain.Pot, PotDiameterCM: 45, Exposure: domain.Outdoor, Location: loc, LastWatered: ago(4), LastFertilized: ago(20)},
		{Nickname: "Lavender hedge", Species: "lavandula", Placement: domain.Ground, Exposure: domain.Outdoor, Location: garden, LastWatered: ago(9), LastFertilized: ago(30)},
		{Nickname: "Monty the Monstera", Species: "monstera-deliciosa", Placement: domain.Pot, PotDiameterCM: 30, Exposure: domain.Indoor, Location: loc, LastWatered: ago(6), LastFertilized: ago(28)},
		{Nickname: "Snake plant", Species: "dracaena-trifasciata", Placement: domain.Pot, PotDiameterCM: 20, Exposure: domain.Indoor, Location: loc, LastWatered: ago(18), LastFertilized: ago(60)},
	}
	for _, p := range demo {
		p.OwnerEmail = email
		if _, err := s.Register(ctx, p); err != nil {
			return err
		}
		time.Sleep(time.Millisecond) // stable created_at ordering
	}
	return nil
}

// buildDiagnoser wires the diagnosis port:
//
//	DIAGNOSIS_PROVIDER=onnx       local fine-tuned model via INFERENCE_URL (default when INFERENCE_URL is set)
//	DIAGNOSIS_PROVIDER=mock       deterministic colour heuristics, no dependencies
//	DIAGNOSIS_PROVIDER=openai|anthropic   vision LLM only
//	SECOND_OPINION=openai|anthropic       ask the LLM when the local model is unsure
func buildDiagnoser() (ports.Diagnoser, error) {
	def := "mock"
	if os.Getenv("INFERENCE_URL") != "" {
		def = "onnx"
	}
	var primary ports.Diagnoser
	switch p := env("DIAGNOSIS_PROVIDER", def); p {
	case "onnx":
		primary = &diagnosis.ONNX{URL: env("INFERENCE_URL", "http://localhost:8000")}
	case "mock":
		primary = diagnosis.Mock{}
	case "openai", "anthropic":
		return llm(p)
	default:
		return nil, errors.New("unknown DIAGNOSIS_PROVIDER " + p)
	}
	if second := os.Getenv("SECOND_OPINION"); second != "" {
		l, err := llm(second)
		if err != nil {
			return nil, err
		}
		return diagnosis.SecondOpinion{Primary: primary, Second: l, Threshold: 0.6}, nil
	}
	return primary, nil
}

func llm(flavor string) (ports.Diagnoser, error) {
	base := map[string]string{"openai": "https://api.openai.com/v1", "anthropic": "https://api.anthropic.com/v1"}[flavor]
	model := map[string]string{"openai": "gpt-4o-mini", "anthropic": "claude-3-5-haiku-latest"}[flavor]
	if base == "" {
		return nil, errors.New("unknown LLM flavor " + flavor)
	}
	if os.Getenv("LLM_API_KEY") == "" {
		return nil, errors.New("LLM_API_KEY is required for the " + flavor + " diagnoser")
	}
	return &diagnosis.LLM{Flavor: diagnosis.Flavor(flavor), BaseURL: env("LLM_BASE_URL", base),
		APIKey: os.Getenv("LLM_API_KEY"), Model: env("LLM_MODEL", model)}, nil
}
