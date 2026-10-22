#!/usr/bin/env python3
"""Download a class-balanced subset of PlantVillage and write it as image folders.

Source: the PlantVillage dataset (Hughes & Salathe, 2015, arXiv:1511.08060),
mirrored on Hugging Face as BrandonFors/Plant-Diseases-PlantVillage-Dataset.

    python ml/download_dataset.py --out data/plantvillage --train-per-class 240 --test-per-class 80

Result:
    data/plantvillage/{train,val,test}/<class>/<n>.jpg
"""
import argparse
import io
import random
import urllib.request
from pathlib import Path

import pyarrow.parquet as pq
from PIL import Image

from classes import CLASSES

BASE = "https://huggingface.co/datasets/BrandonFors/Plant-Diseases-PlantVillage-Dataset/resolve/refs%2Fconvert%2Fparquet/default"
FILES = {  # train shard 0001 holds the tomato classes
    "train": f"{BASE}/train/0001.parquet",
    "test": f"{BASE}/test/0000.parquet",
}
ALL_LABELS_URL = "https://datasets-server.huggingface.co/info?dataset=BrandonFors/Plant-Diseases-PlantVillage-Dataset"


def fetch(url: str, dest: Path) -> Path:
    if dest.exists() and dest.stat().st_size > 0:
        return dest
    dest.parent.mkdir(parents=True, exist_ok=True)
    print(f"downloading {url} -> {dest}")
    urllib.request.urlretrieve(url, dest)
    return dest


def label_names() -> list[str]:
    import json

    with urllib.request.urlopen(ALL_LABELS_URL) as r:
        info = json.load(r)
    return info["dataset_info"]["default"]["features"]["label"]["names"]


def export(parquet: Path, out: Path, per_class: dict[str, int], names: list[str], seed: int) -> None:
    table = pq.read_table(parquet, columns=["label"])
    labels = table.column("label").to_pylist()
    rng = random.Random(seed)
    wanted = {names.index(c): c for c in CLASSES}
    by_class: dict[int, list[int]] = {k: [] for k in wanted}
    for i, lab in enumerate(labels):
        if lab in by_class:
            by_class[lab].append(i)
    picks: dict[int, tuple[str, int]] = {}
    for lab, idxs in by_class.items():
        rng.shuffle(idxs)
        cursor = 0
        for split, n in per_class.items():
            for j, row in enumerate(idxs[cursor : cursor + n]):
                picks[row] = (f"{split}/{wanted[lab]}", j)
            cursor += n
    images = pq.read_table(parquet, columns=["image"]).column("image")
    for row, (folder, j) in picks.items():
        img = Image.open(io.BytesIO(images[row].as_py()["bytes"])).convert("RGB")
        d = out / folder
        d.mkdir(parents=True, exist_ok=True)
        img.save(d / f"{j:04d}.jpg", quality=92)
    print(f"{parquet.name}: wrote {len(picks)} images")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default="data/plantvillage")
    ap.add_argument("--cache", default="data/cache")
    ap.add_argument("--train-per-class", type=int, default=240)
    ap.add_argument("--val-per-class", type=int, default=40)
    ap.add_argument("--test-per-class", type=int, default=80)
    ap.add_argument("--seed", type=int, default=7)
    a = ap.parse_args()
    names = label_names()
    out, cache = Path(a.out), Path(a.cache)
    export(fetch(FILES["train"], cache / "train.parquet"), out,
           {"train": a.train_per_class, "val": a.val_per_class}, names, a.seed)
    export(fetch(FILES["test"], cache / "test.parquet"), out, {"test": a.test_per_class}, names, a.seed)


if __name__ == "__main__":
    main()
