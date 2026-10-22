package diagnosis

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ipenas-cl/leafwise/internal/app"
	"github.com/ipenas-cl/leafwise/internal/domain"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

func png1(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for i := 0; i < 32*32; i++ {
		img.Set(i%32, i/32, c)
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func TestMockIsDeterministicAndSchemaValid(t *testing.T) {
	sc, err := app.CompileSchema()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[color.RGBA]domain.Condition{
		{40, 140, 50, 255}:  domain.Healthy,
		{220, 200, 60, 255}: domain.NutrientDeficiency,
		{140, 90, 40, 255}:  domain.Underwatering,
	}
	for c, want := range cases {
		in := ports.DiagnosisInput{Image: png1(c), CommonName: "Basil"}
		a, _ := Mock{}.Diagnose(context.Background(), in)
		b, _ := Mock{}.Diagnose(context.Background(), in)
		if !bytes.Equal(a, b) {
			t.Fatal("mock must be deterministic")
		}
		got, err := app.ParseAssessment(sc, a)
		if err != nil {
			t.Fatalf("mock output must satisfy the schema: %v", err)
		}
		if got.Primary().Condition != want {
			t.Errorf("colour %v: want %s, got %s", c, want, got.Primary().Condition)
		}
	}
	// Undecodable bytes still produce a valid assessment.
	raw, _ := Mock{}.Diagnose(context.Background(), ports.DiagnosisInput{Image: []byte("RIFF....WEBP")})
	if _, err := app.ParseAssessment(sc, raw); err != nil {
		t.Fatal(err)
	}
}

const validJSON = `{"summary":"Aphids on new growth","findings":[{"condition":"pest","label":"Aphids","confidence":0.8,"evidence":"clusters"}],"actions":[{"priority":"now","description":"Spray water"}]}`

func TestOpenAICompatible(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(503) // transient, should be retried
			return
		}
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("bad request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "data:image/png;base64,") || !strings.Contains(string(body), "json_object") {
			t.Error("request must carry the image and ask for JSON")
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "```json\n" + validJSON + "\n```"}}}})
	}))
	defer srv.Close()
	l := &LLM{Flavor: OpenAI, BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m"}
	raw, err := l.Diagnose(context.Background(), ports.DiagnosisInput{Image: []byte{1}, MIMEType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != validJSON || calls != 2 {
		t.Fatalf("got %s after %d calls", raw, calls)
	}
}

func TestAnthropicCompatible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("bad request %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"media_type":"image/jpeg"`) {
			t.Error("image block missing")
		}
		json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]string{"type": "text", "text": validJSON}}})
	}))
	defer srv.Close()
	l := &LLM{Flavor: Anthropic, BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m"}
	raw, err := l.Diagnose(context.Background(), ports.DiagnosisInput{Image: []byte{1}, MIMEType: "image/jpeg"})
	if err != nil || string(raw) != validJSON {
		t.Fatalf("got %s err=%v", raw, err)
	}
}

func TestLLMDoesNotRetryClientErrors(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(401)
	}))
	defer srv.Close()
	l := &LLM{Flavor: OpenAI, BaseURL: srv.URL, APIKey: "bad", Model: "m"}
	if _, err := l.Diagnose(context.Background(), ports.DiagnosisInput{}); err == nil || calls != 1 {
		t.Fatalf("expected one failed call, got %d err=%v", calls, err)
	}
}
