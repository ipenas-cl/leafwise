#!/usr/bin/env python3
"""Fine-tune MobileNetV3-Small (ImageNet weights) on the PlantVillage tomato subset,
evaluate it on a held-out test split, and export ONNX.

Designed to run on a laptop CPU in a few minutes:

    python ml/download_dataset.py
    python ml/train.py --data data/plantvillage --out ml/model

Outputs in --out:
    leafwise-mnv3s.onnx      the exported model (input: 1x3x224x224 float, ImageNet-normalised)
    labels.json              class names in output order + preprocessing
    metrics.json             accuracy, macro-F1, per-class precision/recall/F1
    confusion_matrix.png     test-set confusion matrix
    training_curve.png       loss/accuracy per epoch
"""
import argparse
import json
import time
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
import torch
from sklearn.metrics import accuracy_score, classification_report, confusion_matrix, f1_score
from torch import nn
from torch.utils.data import DataLoader
from torchvision import datasets, models, transforms

from classes import CLASSES

MEAN, STD, SIZE = [0.485, 0.456, 0.406], [0.229, 0.224, 0.225], 224


def loaders(root: Path, batch: int, workers: int):
    train_tf = transforms.Compose([
        transforms.RandomResizedCrop(SIZE, scale=(0.6, 1.0)),
        transforms.RandomHorizontalFlip(),
        transforms.RandomVerticalFlip(),
        transforms.ColorJitter(0.3, 0.3, 0.2, 0.02),
        transforms.RandomRotation(20),
        transforms.ToTensor(),
        transforms.Normalize(MEAN, STD),
    ])
    eval_tf = transforms.Compose([
        transforms.Resize(256), transforms.CenterCrop(SIZE), transforms.ToTensor(), transforms.Normalize(MEAN, STD),
    ])
    sets = {s: datasets.ImageFolder(root / s, train_tf if s == "train" else eval_tf) for s in ("train", "val", "test")}
    for s, ds in sets.items():
        assert ds.classes == sorted(CLASSES), f"{s}: unexpected classes {ds.classes}"
    return {s: DataLoader(ds, batch_size=batch, shuffle=(s == "train"), num_workers=workers, persistent_workers=workers > 0) for s, ds in sets.items()}, sets["train"].classes


def predict(model, loader):
    model.eval()
    ys, ps = [], []
    with torch.no_grad():
        for x, y in loader:
            ps.append(model(x).argmax(1).numpy())
            ys.append(y.numpy())
    return np.concatenate(ys), np.concatenate(ps)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", default="data/plantvillage")
    ap.add_argument("--out", default="ml/model")
    ap.add_argument("--epochs", type=int, default=12)
    ap.add_argument("--batch", type=int, default=32)
    ap.add_argument("--lr", type=float, default=1e-3)
    ap.add_argument("--seed", type=int, default=7)
    ap.add_argument("--workers", type=int, default=2)
    a = ap.parse_args()
    torch.manual_seed(a.seed)
    torch.set_num_threads(max(1, torch.get_num_threads()))
    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)

    dl, classes = loaders(Path(a.data), a.batch, a.workers)
    model = models.mobilenet_v3_small(weights=models.MobileNet_V3_Small_Weights.IMAGENET1K_V1)
    model.classifier[3] = nn.Linear(model.classifier[3].in_features, len(classes))

    # Transfer learning: freeze the early feature blocks, train the last blocks + head.
    for p in model.features[:9].parameters():
        p.requires_grad = False
    params = [p for p in model.parameters() if p.requires_grad]
    opt = torch.optim.AdamW(params, lr=a.lr, weight_decay=1e-4)
    sched = torch.optim.lr_scheduler.CosineAnnealingLR(opt, T_max=a.epochs)
    loss_fn = nn.CrossEntropyLoss(label_smoothing=0.1)

    history, best, best_state = [], -1.0, None
    for epoch in range(1, a.epochs + 1):
        model.train()
        t0, total, n = time.time(), 0.0, 0
        for x, y in dl["train"]:
            opt.zero_grad()
            loss = loss_fn(model(x), y)
            loss.backward()
            opt.step()
            total += loss.item() * len(y)
            n += len(y)
        sched.step()
        yv, pv = predict(model, dl["val"])
        val_acc = accuracy_score(yv, pv)
        history.append({"epoch": epoch, "train_loss": total / n, "val_acc": val_acc})
        print(f"epoch {epoch}: loss {total / n:.3f} val_acc {val_acc:.3f} ({time.time() - t0:.0f}s)", flush=True)
        if val_acc > best:
            best, best_state = val_acc, {k: v.clone() for k, v in model.state_dict().items()}
    model.load_state_dict(best_state)

    # --- Evaluation on the held-out test split -------------------------------
    yt, pt = predict(model, dl["test"])
    short = [c.split("___")[1].replace("_", " ") for c in classes]
    report = classification_report(yt, pt, target_names=short, output_dict=True, zero_division=0)
    metrics = {
        "test_accuracy": accuracy_score(yt, pt),
        "test_macro_f1": f1_score(yt, pt, average="macro"),
        "test_images": int(len(yt)),
        "train_images": len(dl["train"].dataset),
        "val_images": len(dl["val"].dataset),
        "epochs": a.epochs,
        "best_val_accuracy": best,
        "per_class": {k: {m: round(v, 4) for m, v in report[k].items()} for k in short},
        "history": history,
    }
    (out / "metrics.json").write_text(json.dumps(metrics, indent=2))
    print(json.dumps({k: metrics[k] for k in ("test_accuracy", "test_macro_f1", "test_images")}))

    cm = confusion_matrix(yt, pt)
    fig, ax = plt.subplots(figsize=(9, 8))
    im = ax.imshow(cm, cmap="Greens")
    ax.set_xticks(range(len(short)), short, rotation=45, ha="right")
    ax.set_yticks(range(len(short)), short)
    for i in range(len(short)):
        for j in range(len(short)):
            ax.text(j, i, cm[i, j], ha="center", va="center", color="white" if cm[i, j] > cm.max() / 2 else "black", fontsize=9)
    ax.set_xlabel("Predicted")
    ax.set_ylabel("True")
    ax.set_title(f"Leafwise MobileNetV3-Small, test accuracy {metrics['test_accuracy']:.1%}")
    fig.colorbar(im, fraction=0.046)
    fig.tight_layout()
    fig.savefig(out / "confusion_matrix.png", dpi=110)

    fig, ax1 = plt.subplots(figsize=(6, 3.5))
    ep = [h["epoch"] for h in history]
    ax1.plot(ep, [h["train_loss"] for h in history], "o-", color="#2f6b3a", label="train loss")
    ax2 = ax1.twinx()
    ax2.plot(ep, [h["val_acc"] for h in history], "s--", color="#c0582b", label="val accuracy")
    ax1.set_xlabel("epoch")
    ax1.set_ylabel("loss")
    ax2.set_ylabel("accuracy")
    fig.legend(loc="center right")
    fig.tight_layout()
    fig.savefig(out / "training_curve.png", dpi=110)

    # --- Export ONNX -----------------------------------------------------------
    model.eval()
    dummy = torch.randn(1, 3, SIZE, SIZE)
    torch.onnx.export(model, dummy, out / "leafwise-mnv3s.onnx", input_names=["image"], output_names=["logits"],
                      dynamic_axes={"image": {0: "batch"}, "logits": {0: "batch"}}, opset_version=17, dynamo=False)
    (out / "labels.json").write_text(json.dumps({
        "model": "leafwise-mnv3s", "architecture": "mobilenet_v3_small", "classes": classes,
        "input": {"size": SIZE, "resize": 256, "mean": MEAN, "std": STD, "layout": "NCHW"},
    }, indent=2))
    print("exported", out / "leafwise-mnv3s.onnx")


if __name__ == "__main__":
    main()
