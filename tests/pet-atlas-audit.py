"""Read-only sprite audit and reproducible render metadata. Requires Pillow.

python3 tests/pet-atlas-audit.py [--write]
The WebP is never rewritten. --write updates only the JS render metadata.
"""
import argparse
import json
import math
from pathlib import Path
import re
import statistics

from PIL import Image

ROOT = Path(__file__).resolve().parents[1]
META = ROOT / "web/pet-atlas.js"
MARKER = "// Render calibration (tests/pet-atlas-audit.py --write)."

# These source drawings show only the part above an illustrated ledge. That ledge
# is absent in the app, so use complete poses instead of displaying floating heads.
FALLBACKS = {
    "jumpup_4": "touch_3", "jumpdown_1": "leap_1",
    "peek_1": "ledge_1", "peek_2": "ledge_2",
    "edgethink_1": "ledge_1", "edgethink_2": "ledge_2",
    "edgethink_3": "ledge_1", "edgethink_4": "ledge_2",
    "lookdown_1": "ledge_1", "lookdown_2": "ledge_2", "lookdown_3": "ledge_1",
    "climb_1": "paw_3", "climb_2": "paw_4", "climb_3": "reach_2", "climb_4": "paw_4",
}


def audit():
    source = META.read_text()
    base = source.split(MARKER)[0].rstrip() + "\n"
    frames = json.loads(re.search(r"frames: (.*) };", base).group(1))
    image = Image.open(ROOT / "web/cat-atlas.webp").convert("RGBA")
    areas, colours, families = {}, {}, {}
    for name, frame in frames.items():
        x, y, w, h, ax, top, *below = frame
        assert 0 <= x < x + w <= image.width and 0 <= y < y + h <= image.height, name
        assert 0 <= ax <= w and top in (0, 1), name
        pixels = list(image.crop((x, y, x + w, y + h)).get_flattened_data())
        core = [p for p in pixels if p[3] == 255]
        assert core, name
        # Exclude eyes, pink ears, cream highlights and transparent background.
        fur = [p for p in core if 45 < sum(p[:3]) / 3 < 210 and max(p[:3]) - min(p[:3]) < 45]
        assert fur, name
        areas[name] = len(core)
        colours[name] = [statistics.median(p[c] for p in fur) for c in range(3)]
        family = name.rsplit("_", 1)[0]
        if name not in FALLBACKS:
            families.setdefault(family, []).append(len(core))
    reference = colours["sit_1"]
    render = {}
    for name in frames:
        family = name.rsplit("_", 1)[0]
        area = statistics.median(families.get(family, [areas[name]]))
        # Preserve real crouching/stretching; only correct modest drawing drift.
        scale = max(.9, min(1.1, math.sqrt(area / areas[name])))
        gains = [max(.88, min(1.12, reference[c] / colours[name][c])) for c in range(3)]
        render[name] = [round(scale, 4), *[round(g, 4) for g in gains]]
    for name, target in FALLBACKS.items():
        assert name in frames and target in frames and target not in FALLBACKS
    compact = lambda x: json.dumps(x, ensure_ascii=False, separators=(",", ":"))
    generated = (base + MARKER + "\nPET_ATLAS.render = " + compact(render) + ";\n"
                 + "PET_ATLAS.fallbacks = " + compact(FALLBACKS) + ";\n")
    return source, generated, frames, image


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--write", action="store_true")
    args = parser.parse_args()
    source, generated, frames, image = audit()
    if args.write:
        META.write_text(generated)
    else:
        assert source == generated, "Render metadata is stale; run with --write"
    print(f"{len(frames)} frames: bounds, anchors, fur palette and scale calibration OK")
    print(f"{len(FALLBACKS)} cropped poses replaced; source WebP unchanged")
