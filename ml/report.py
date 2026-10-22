#!/usr/bin/env python3
"""Print the evaluation as a Markdown table (pasted into README and MODEL_CARD)."""
import json
import sys
from pathlib import Path

m = json.loads(Path(sys.argv[1] if len(sys.argv) > 1 else "ml/model/metrics.json").read_text())
print(f"Test accuracy **{m['test_accuracy']:.1%}**, macro-F1 **{m['test_macro_f1']:.3f}** "
      f"on {m['test_images']} held-out images ({m['train_images']} train / {m['val_images']} val, {m['epochs']} epochs).\n")
print("| Class | Precision | Recall | F1 | Test images |")
print("|---|---:|---:|---:|---:|")
for name, r in m["per_class"].items():
    print(f"| {name} | {r['precision']:.3f} | {r['recall']:.3f} | {r['f1-score']:.3f} | {int(r['support'])} |")
