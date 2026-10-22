#!/usr/bin/env python3
"""Leafwise leaf-disease inference service (ONNX Runtime, CPU).

A deliberately small HTTP server with no web framework:

    POST /predict      body: raw JPEG/PNG/WebP bytes -> top-k class probabilities
    GET  /metadata     model name, classes and preprocessing
    GET  /healthz      liveness

The Go API talks to it through the `onnx` adapter of the diagnosis port.
"""
import io
import json
import os
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import numpy as np
import onnxruntime as ort
from PIL import Image, UnidentifiedImageError

MODEL_DIR = Path(os.environ.get("MODEL_DIR", Path(__file__).resolve().parent.parent / "model"))
MAX_BYTES = 10 << 20


class Classifier:
    def __init__(self, model_dir: Path = MODEL_DIR):
        meta = json.loads((model_dir / "labels.json").read_text())
        self.meta = meta
        self.classes: list[str] = meta["classes"]
        cfg = meta["input"]
        self.size, self.resize = cfg["size"], cfg["resize"]
        self.mean = np.array(cfg["mean"], dtype=np.float32).reshape(3, 1, 1)
        self.std = np.array(cfg["std"], dtype=np.float32).reshape(3, 1, 1)
        opts = ort.SessionOptions()
        opts.intra_op_num_threads = int(os.environ.get("ORT_THREADS", "2"))
        self.session = ort.InferenceSession(str(model_dir / f"{meta['model']}.onnx"), opts, providers=["CPUExecutionProvider"])

    def preprocess(self, data: bytes) -> np.ndarray:
        """Same as torchvision Resize(256) + CenterCrop(224) + Normalize."""
        img = Image.open(io.BytesIO(data)).convert("RGB")
        w, h = img.size
        scale = self.resize / min(w, h)
        img = img.resize((max(1, round(w * scale)), max(1, round(h * scale))), Image.BILINEAR)
        w, h = img.size
        left, top = (w - self.size) // 2, (h - self.size) // 2
        img = img.crop((left, top, left + self.size, top + self.size))
        x = np.asarray(img, dtype=np.float32).transpose(2, 0, 1) / 255.0
        return ((x - self.mean) / self.std)[None].astype(np.float32)

    def predict(self, data: bytes, k: int = 3) -> dict:
        t0 = time.perf_counter()
        logits = self.session.run(None, {"image": self.preprocess(data)})[0][0]
        e = np.exp(logits - logits.max())
        probs = e / e.sum()
        top = np.argsort(-probs)[:k]
        return {
            "model": self.meta["model"],
            "predictions": [{"class": self.classes[i], "probability": round(float(probs[i]), 4)} for i in top],
            "latencyMs": round((time.perf_counter() - t0) * 1000, 1),
        }


def make_handler(clf: Classifier):
    class Handler(BaseHTTPRequestHandler):
        def _json(self, code: int, body: dict) -> None:
            payload = json.dumps(body).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def do_GET(self):  # noqa: N802
            if self.path == "/healthz":
                return self._json(200, {"status": "ok"})
            if self.path == "/metadata":
                return self._json(200, clf.meta)
            self._json(404, {"error": "not found"})

        def do_POST(self):  # noqa: N802
            if self.path.split("?")[0] != "/predict":
                return self._json(404, {"error": "not found"})
            n = int(self.headers.get("Content-Length", 0))
            if n <= 0 or n > MAX_BYTES:
                return self._json(400, {"error": "body must be an image up to 10 MB"})
            try:
                return self._json(200, clf.predict(self.rfile.read(n)))
            except (UnidentifiedImageError, OSError):
                return self._json(400, {"error": "not a decodable image"})

        def log_message(self, fmt, *args):  # quieter, one JSON line per request
            print(json.dumps({"msg": "request", "line": fmt % args}), flush=True)

    return Handler


def main() -> None:
    clf = Classifier()
    addr = ("0.0.0.0", int(os.environ.get("PORT", "8000")))
    print(json.dumps({"msg": "serving", "model": clf.meta["model"], "addr": f"{addr[0]}:{addr[1]}"}), flush=True)
    ThreadingHTTPServer(addr, make_handler(clf)).serve_forever()


if __name__ == "__main__":
    main()
