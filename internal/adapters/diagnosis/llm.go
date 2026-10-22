package diagnosis

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ipenas-cl/leafwise/api"
	"github.com/ipenas-cl/leafwise/internal/ports"
)

// Flavor selects the wire protocol of the vision LLM.
type Flavor string

const (
	OpenAI    Flavor = "openai"    // POST {base}/chat/completions (OpenAI, Azure, Groq, Ollama, vLLM...)
	Anthropic Flavor = "anthropic" // POST {base}/messages
)

// LLM is a diagnoser backed by any OpenAI- or Anthropic-compatible vision model.
type LLM struct {
	Flavor  Flavor
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

func (l *LLM) Name() string { return fmt.Sprintf("%s:%s", l.Flavor, l.Model) }

func prompt(in ports.DiagnosisInput) string {
	return fmt.Sprintf(`You are a plant pathologist. Look at the photo of a %s (%s, %s, %s).
Identify pests, diseases, overwatering, underwatering or nutrient deficiencies.
Reply with ONLY a JSON object that validates against this JSON Schema, no prose, no code fences:
%s`, in.CommonName, in.Species, in.Exposure, in.Placement, string(api.DiagnosisSchema))
}

func (l *LLM) Diagnose(ctx context.Context, in ports.DiagnosisInput) (json.RawMessage, error) {
	b64 := base64.StdEncoding.EncodeToString(in.Image)
	var url string
	var body any
	switch l.Flavor {
	case Anthropic:
		url = strings.TrimRight(l.BaseURL, "/") + "/messages"
		body = map[string]any{
			"model": l.Model, "max_tokens": 1024,
			"messages": []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": in.MIMEType, "data": b64}},
				map[string]any{"type": "text", "text": prompt(in)},
			}}},
		}
	default:
		url = strings.TrimRight(l.BaseURL, "/") + "/chat/completions"
		body = map[string]any{
			"model":           l.Model,
			"response_format": map[string]string{"type": "json_object"},
			"messages": []any{map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": prompt(in)},
				map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + in.MIMEType + ";base64," + b64}},
			}}},
		}
	}
	payload, _ := json.Marshal(body)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 500 * time.Millisecond):
			}
		}
		raw, retry, err := l.call(ctx, url, payload)
		if err == nil {
			return raw, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return nil, lastErr
}

func (l *LLM) call(ctx context.Context, url string, payload []byte) (json.RawMessage, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if l.Flavor == Anthropic {
		req.Header.Set("x-api-key", l.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+l.APIKey)
	}
	client := l.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("llm: status %d", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, false, fmt.Errorf("llm: status %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	var text string
	if l.Flavor == Anthropic {
		var r struct {
			Content []struct{ Type, Text string } `json:"content"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, false, err
		}
		for _, c := range r.Content {
			if c.Type == "text" {
				text += c.Text
			}
		}
	} else {
		var r struct {
			Choices []struct {
				Message struct{ Content string } `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, false, err
		}
		if len(r.Choices) == 0 {
			return nil, false, errors.New("llm: no choices")
		}
		text = r.Choices[0].Message.Content
	}
	return json.RawMessage(extractJSON(text)), false, nil
}

// extractJSON tolerates models that wrap JSON in code fences or prose.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
