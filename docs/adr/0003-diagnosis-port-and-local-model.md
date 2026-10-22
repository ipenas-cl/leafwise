# ADR 0003: Local ONNX model first, vision LLM as second opinion

- Status: accepted
- Date: 2026-10-22

## Context

Photo diagnosis has to work offline in the demo, be cheap per request, give
calibrated confidences, and still handle cases a small model has never seen.

## Decision

- A `Diagnoser` port returns raw JSON that **must** validate against
  `api/diagnosis.schema.json`; the core validates every response and rejects
  anything else (HTTP 502). Adapters cannot leak free-form text into the DB.
- Default adapter `onnx`: a MobileNetV3-Small fine-tuned on PlantVillage tomato
  leaves (10 classes), exported to ONNX and served by a small ONNX Runtime
  service (`ml/serve`). The Go adapter maps class probabilities to findings and
  agronomic actions from `knowledge.json`.
- Optional `SECOND_OPINION=openai|anthropic`: when the local model's top
  confidence is below 0.6, an OpenAI- or Anthropic-compatible vision model is
  asked, and both opinions are merged. If the local model is down, it fails
  over to the LLM.
- `mock` adapter: deterministic colour heuristics for unit tests.

## Why a separate inference service instead of onnxruntime in Go

The Go API stays a static, CGO-free binary on distroless. The model server can
scale and be redeployed independently (e.g. on GPU nodes), and the same
`/predict` contract works for a future Triton or KServe deployment.

## Consequences

- Real inference works with no API keys (~10 ms per image on CPU).
- The model only knows tomato leaves photographed on plain backgrounds; the
  summary says so for other species and low-confidence results. See
  `ml/MODEL_CARD.md`.
