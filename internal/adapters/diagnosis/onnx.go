package diagnosis

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ipenas-cl/leafwise/internal/domain"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

//go:embed knowledge.json
var knowledgeJSON []byte

type classInfo struct {
	Condition domain.Condition `json:"condition"`
	Label     string           `json:"label"`
	Evidence  string           `json:"evidence"`
	Actions   [][2]string      `json:"actions"`
}

// knowledge maps model classes to agronomic advice. Keeping it in the
// adapter (not in the model server) means the model can be retrained or
// swapped without touching the advice, and vice versa.
var knowledge = func() map[string]classInfo {
	m := map[string]classInfo{}
	if err := json.Unmarshal(knowledgeJSON, &m); err != nil {
		panic(err)
	}
	return m
}()

// ONNX is the default diagnoser: a fine-tuned MobileNetV3-Small leaf disease
// classifier served by ONNX Runtime (see ml/). It turns top-k class
// probabilities into a schema-valid assessment.
type ONNX struct {
	URL  string // e.g. http://inference:8000
	HTTP *http.Client
}

func (o *ONNX) Name() string { return "onnx:leafwise-mnv3s" }

type prediction struct {
	Class       string  `json:"class"`
	Probability float64 `json:"probability"`
}

// Predict calls the inference service.
func (o *ONNX) Predict(ctx context.Context, img []byte, mime string) ([]prediction, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.URL, "/")+"/predict", bytes.NewReader(img))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mime)
	c := o.HTTP
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("inference: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("inference: status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Predictions []prediction `json:"predictions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Predictions) == 0 {
		return nil, fmt.Errorf("inference: empty prediction")
	}
	return out.Predictions, nil
}

func (o *ONNX) Diagnose(ctx context.Context, in ports.DiagnosisInput) (json.RawMessage, error) {
	preds, err := o.Predict(ctx, in.Image, in.MIMEType)
	if err != nil {
		return nil, err
	}
	return json.Marshal(assessmentFromPredictions(preds, in))
}

func assessmentFromPredictions(preds []prediction, in ports.DiagnosisInput) domain.Assessment {
	var a domain.Assessment
	seen := map[string]bool{}
	for i, p := range preds {
		info, ok := knowledge[p.Class]
		if !ok || seen[info.Label] || (i > 0 && p.Probability < 0.10) {
			continue
		}
		seen[info.Label] = true
		a.Findings = append(a.Findings, domain.Finding{Condition: info.Condition, Label: info.Label,
			Confidence: p.Probability, Evidence: info.Evidence})
	}
	if len(a.Findings) == 0 {
		a.Findings = []domain.Finding{{Condition: domain.Healthy, Label: "Unrecognised", Confidence: 0, Evidence: "The model did not recognise this image."}}
	}
	top := knowledge[preds[0].Class]
	for _, ac := range top.Actions {
		a.Actions = append(a.Actions, domain.Action{Priority: ac[0], Description: ac[1]})
	}
	conf := preds[0].Probability
	switch {
	case top.Condition == domain.Healthy:
		a.Summary = fmt.Sprintf("The leaf looks healthy (%.0f%% confidence).", conf*100)
	default:
		a.Summary = fmt.Sprintf("Signs of %s (%.0f%% confidence).", top.Label, conf*100)
	}
	if conf < 0.6 {
		a.Summary += " Confidence is low: retake the photo of a single leaf in daylight."
	}
	if !strings.HasPrefix(preds[0].Class, "Tomato") || (in.Species != "" && in.Species != "solanum-lycopersicum") {
		a.Summary += " Note: the local model is trained on tomato leaves; results for other species are indicative."
	}
	return a
}

// SecondOpinion runs the primary diagnoser and, when its top confidence is
// below Threshold, asks a second one (typically a vision LLM) and merges the
// findings. If the second opinion fails, the primary result still stands.
type SecondOpinion struct {
	Primary, Second ports.Diagnoser
	Threshold       float64
}

func (s SecondOpinion) Name() string { return s.Primary.Name() + "+" + s.Second.Name() }

func (s SecondOpinion) Diagnose(ctx context.Context, in ports.DiagnosisInput) (json.RawMessage, error) {
	raw, err := s.Primary.Diagnose(ctx, in)
	if err != nil {
		return s.Second.Diagnose(ctx, in) // fail over
	}
	var a domain.Assessment
	if json.Unmarshal(raw, &a) != nil || a.Primary().Confidence >= s.Threshold {
		return raw, nil
	}
	raw2, err := s.Second.Diagnose(ctx, in)
	if err != nil {
		return raw, nil
	}
	var b domain.Assessment
	if json.Unmarshal(raw2, &b) != nil {
		return raw, nil
	}
	b.Summary = "Second opinion: " + b.Summary + " (local model: " + a.Summary + ")"
	if len(b.Summary) > 500 {
		b.Summary = b.Summary[:497] + "..."
	}
	for _, f := range a.Findings {
		if len(b.Findings) < 5 {
			f.Evidence = "local model: " + f.Evidence
			b.Findings = append(b.Findings, f)
		}
	}
	return json.Marshal(b)
}
