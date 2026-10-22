package diagnosis

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ipenas-cl/leafwise/internal/app"
	"github.com/ipenas-cl/leafwise/internal/domain"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

func TestKnowledgeCoversEveryModelClass(t *testing.T) {
	if len(knowledge) != 10 {
		t.Fatalf("expected 10 classes, got %d", len(knowledge))
	}
	for c, k := range knowledge {
		if k.Label == "" || len(k.Actions) == 0 {
			t.Errorf("%s: incomplete knowledge", c)
		}
	}
}

func inferenceServer(t *testing.T, preds string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/predict" || len(b) == 0 || r.Header.Get("Content-Type") != "image/jpeg" {
			t.Errorf("bad request %s", r.URL.Path)
		}
		w.Write([]byte(`{"model":"leafwise-mnv3s","predictions":` + preds + `}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestONNXAdapterProducesSchemaValidAssessment(t *testing.T) {
	srv := inferenceServer(t, `[{"class":"Tomato___Early_blight","probability":0.91},{"class":"Tomato___Target_Spot","probability":0.06},{"class":"Tomato___healthy","probability":0.01}]`)
	raw, err := (&ONNX{URL: srv.URL}).Diagnose(context.Background(), ports.DiagnosisInput{Image: []byte{1}, MIMEType: "image/jpeg", Species: "solanum-lycopersicum"})
	if err != nil {
		t.Fatal(err)
	}
	sc, _ := app.CompileSchema()
	a, err := app.ParseAssessment(sc, raw)
	if err != nil {
		t.Fatalf("schema: %v\n%s", err, raw)
	}
	if p := a.Primary(); p.Condition != domain.Disease || p.Label != "Early blight (Alternaria)" || p.Confidence != 0.91 {
		t.Fatalf("unexpected primary %+v", p)
	}
	if len(a.Findings) != 1 { // tiny probabilities are dropped
		t.Fatalf("expected 1 finding, got %d", len(a.Findings))
	}
}

func TestONNXLowConfidenceAndOtherSpeciesAreFlagged(t *testing.T) {
	srv := inferenceServer(t, `[{"class":"Tomato___Leaf_Mold","probability":0.41},{"class":"Tomato___Septoria_leaf_spot","probability":0.33}]`)
	raw, _ := (&ONNX{URL: srv.URL}).Diagnose(context.Background(), ports.DiagnosisInput{Image: []byte{1}, MIMEType: "image/jpeg", Species: "rosa"})
	var a domain.Assessment
	json.Unmarshal(raw, &a)
	if len(a.Findings) != 2 {
		t.Fatalf("expected both plausible findings, got %+v", a.Findings)
	}
	for _, want := range []string{"Confidence is low", "trained on tomato"} {
		if !contains(a.Summary, want) {
			t.Errorf("summary %q should mention %q", a.Summary, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

type fixed struct {
	name string
	a    domain.Assessment
	err  error
}

func (f fixed) Name() string { return f.name }
func (f fixed) Diagnose(context.Context, ports.DiagnosisInput) (json.RawMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	return json.Marshal(f.a)
}

func assess(c domain.Condition, conf float64) domain.Assessment {
	return domain.Assessment{Summary: string(c), Findings: []domain.Finding{{Condition: c, Label: string(c), Confidence: conf}},
		Actions: []domain.Action{{Priority: "now", Description: "x"}}}
}

func TestSecondOpinion(t *testing.T) {
	ctx := context.Background()
	llm := fixed{name: "llm", a: assess(domain.Pest, 0.8)}
	// Confident primary: no second call needed.
	sure := SecondOpinion{Primary: fixed{name: "onnx", a: assess(domain.Disease, 0.95)}, Second: fixed{err: errors.New("must not be called")}, Threshold: 0.6}
	raw, _ := sure.Diagnose(ctx, ports.DiagnosisInput{})
	var a domain.Assessment
	json.Unmarshal(raw, &a)
	if a.Primary().Condition != domain.Disease {
		t.Fatal("confident primary should stand")
	}
	// Unsure primary: merge the LLM's opinion first.
	unsure := SecondOpinion{Primary: fixed{name: "onnx", a: assess(domain.Disease, 0.4)}, Second: llm, Threshold: 0.6}
	raw, _ = unsure.Diagnose(ctx, ports.DiagnosisInput{})
	json.Unmarshal(raw, &a)
	if a.Primary().Condition != domain.Pest || len(a.Findings) != 2 {
		t.Fatalf("expected merged findings, got %+v", a)
	}
	// Primary down: fail over to the second.
	down := SecondOpinion{Primary: fixed{err: errors.New("inference down")}, Second: llm, Threshold: 0.6}
	if raw, err := down.Diagnose(ctx, ports.DiagnosisInput{}); err != nil || raw == nil {
		t.Fatalf("expected failover, got %v", err)
	}
}
