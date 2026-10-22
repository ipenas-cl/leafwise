"""Tests for the inference service. They run against the real ONNX model."""
import io
import json
import threading
import urllib.request
from http.server import ThreadingHTTPServer
from pathlib import Path

import numpy as np
import pytest
from PIL import Image

from server import Classifier, make_handler

SAMPLES = Path(__file__).resolve().parent.parent / "samples"


@pytest.fixture(scope="module")
def clf():
    return Classifier()


def test_preprocess_shape_and_normalisation(clf):
    buf = io.BytesIO()
    Image.new("RGB", (640, 480), (124, 116, 104)).save(buf, "PNG")  # ~ImageNet mean colour
    x = clf.preprocess(buf.getvalue())
    assert x.shape == (1, 3, 224, 224) and x.dtype == np.float32
    assert abs(float(x.mean())) < 0.05


@pytest.mark.parametrize("path", sorted(SAMPLES.glob("*.jpg")), ids=lambda p: p.stem)
def test_sample_images_are_classified_correctly(clf, path):
    out = clf.predict(path.read_bytes())
    top = out["predictions"][0]
    expected = path.stem.rsplit("-", 1)[0]
    assert top["class"].split("___")[1].startswith(expected), out
    assert 0 < top["probability"] <= 1
    assert sum(p["probability"] for p in out["predictions"]) <= 1.0001


def test_http_api(clf):
    srv = ThreadingHTTPServer(("127.0.0.1", 0), make_handler(clf))
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    base = f"http://127.0.0.1:{srv.server_port}"
    try:
        sample = next(SAMPLES.glob("*.jpg")).read_bytes()
        req = urllib.request.Request(base + "/predict", data=sample, headers={"Content-Type": "image/jpeg"})
        body = json.load(urllib.request.urlopen(req))
        assert body["model"] == "leafwise-mnv3s" and len(body["predictions"]) == 3
        bad = urllib.request.Request(base + "/predict", data=b"not an image")
        with pytest.raises(urllib.error.HTTPError) as e:
            urllib.request.urlopen(bad)
        assert e.value.code == 400
        assert json.load(urllib.request.urlopen(base + "/healthz"))["status"] == "ok"
    finally:
        srv.shutdown()
