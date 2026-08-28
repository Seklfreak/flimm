#!/usr/bin/env python3
"""Renders every piece of Flimm's icon art from one definition.

The mark is a play triangle with a ghost trail behind it — "flimmern", the
flicker of a screen — the same shape the web sidebar draws inline
(`frontend/src/components/Layout.tsx`) and the favicon carries
(`frontend/public/favicon.svg`).

Everything here is generated rather than hand-drawn so the glyph is the same
size, colour and optical centre everywhere, and so a change to the mark is one
edit and a re-run instead of a dozen exported PNGs:

    brew install librsvg          # provides rsvg-convert
    python3 scripts/make-icons.py

Sizing is the thing this script exists to keep honest. An app icon is read at
40pt on a home screen, so the glyph fills a *fraction of the canvas* stated
below rather than whatever a drawing tool happened to export; the first
version of these icons sat at ~26% and read as a small mark adrift in a blue
square.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
APPLE = ROOT / "apple"

ACCENT = "#2f6df6"

# The mark in its own coordinates: the triangle, and the trail one step behind
# it. Stroke and round joins are what give the shape its soft corners, so the
# drawn bounds are the path plus half the stroke on every side.
TRIANGLE = "M27 20L44 32L27 44Z"
TRAIL_DX = -8
STROKE = 5
TRAIL_OPACITY = 0.38
BOUNDS = (27 + TRAIL_DX - STROKE / 2, 20 - STROKE / 2, 44 + STROKE / 2, 44 + STROKE / 2)
GLYPH_W = BOUNDS[2] - BOUNDS[0]
GLYPH_H = BOUNDS[3] - BOUNDS[1]

# A lone play triangle needs nudging right to look centred, its mass being on
# the left edge — but the trail sits on that side and balances it, so the mark
# as a whole is centred on its bounds and only barely nudged.
OPTICAL_NUDGE = 0.005


def glyph(width: float, height: float, size: float, along: str, parts: str) -> str:
    """The mark, scaled so its drawn bounds are `size` of the canvas.

    `along` picks which canvas dimension `size` is a fraction of: width for a
    square icon, height for the wide tvOS canvases, where the height is what
    runs out first. `parts` is "both", "trail" or "play" — the tvOS layered
    icon needs each on its own layer so the parallax can separate them.
    """
    scale = (width * size if along == "width" else height * size) / (GLYPH_W if along == "width" else GLYPH_H)
    x = (width - GLYPH_W * scale) / 2 - BOUNDS[0] * scale + width * OPTICAL_NUDGE
    y = (height - GLYPH_H * scale) / 2 - BOUNDS[1] * scale
    shapes = []
    if parts in ("both", "trail"):
        shapes.append(f'<path d="{TRIANGLE}" transform="translate({TRAIL_DX} 0)" opacity="{TRAIL_OPACITY}"/>')
    if parts in ("both", "play"):
        shapes.append(f'<path d="{TRIANGLE}"/>')
    return (
        f'<g transform="translate({x:.3f} {y:.3f}) scale({scale:.5f})" fill="#fff" stroke="#fff" '
        f'stroke-width="{STROKE}" stroke-linejoin="round">{"".join(shapes)}</g>'
    )


def render(path: Path, width: int, height: int, size: float, along: str, parts: str, background: bool) -> None:
    body = f'<rect width="{width}" height="{height}" fill="{ACCENT}"/>' if background else ""
    body += glyph(width, height, size, along, parts) if parts != "none" else ""
    svg = f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">{body}</svg>'
    path.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        ["rsvg-convert", "-w", str(width), "-h", str(height), "-o", str(path)],
        input=svg.encode(),
        check=True,
    )
    print(f"  {path.relative_to(ROOT)}  {width}×{height}")


def main() -> int:
    if subprocess.run(["which", "rsvg-convert"], capture_output=True).returncode != 0:
        print("rsvg-convert not found — brew install librsvg", file=sys.stderr)
        return 1

    # iPhone / iPad: one 1024 master, masked to the platform's shape by iOS.
    print("iOS app icon")
    render(APPLE / "Flimm/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png",
           1024, 1024, size=0.62, along="width", parts="both", background=True)

    # tvOS layered icon. The parallax pulls the layers apart on focus, so the
    # trail and the triangle are on layers of their own; each is drawn on the
    # full canvas so they stay registered. tvOS crops the outer ~10% as it
    # tilts, which is why the glyph is a little over half the height.
    print("tvOS layered icon")
    for stack, w, h in (("App Icon.imagestack", 400, 240), ("App Icon - App Store.imagestack", 1280, 768)):
        base = APPLE / "FlimmTV/Assets.xcassets/App Icon & Top Shelf Image.brandassets" / stack
        scales = (1, 2) if stack == "App Icon.imagestack" else (1,)
        for layer, parts, background in (("Back", "none", True), ("Middle", "trail", False), ("Front", "play", False)):
            for scale in scales:
                name = f"{layer.lower()}@{scale}x.png"
                render(base / f"{layer}.imagestacklayer/Content.imageset" / name,
                       w * scale, h * scale, size=0.55, along="height", parts=parts, background=background)

    # Top shelf: seen at a distance across a TV, but it is a banner rather than
    # an icon, so the mark sits smaller in a lot of colour.
    print("tvOS top shelf")
    shelf = APPLE / "FlimmTV/Assets.xcassets/App Icon & Top Shelf Image.brandassets"
    for name, w, h in (("Top Shelf Image.imageset", 1920, 720), ("Top Shelf Image Wide.imageset", 2320, 720)):
        for scale in (1, 2):
            render(shelf / name / f"topshelf@{scale}x.png",
                   w * scale, h * scale, size=0.40, along="height", parts="both", background=True)

    # The favicon is the same mark on its own rounded square, because a browser
    # tab does not mask it for us.
    print("web favicon")
    fav = ROOT / "frontend/public/favicon.svg"
    body = f'<rect width="64" height="64" rx="15" fill="{ACCENT}"/>' + glyph(64, 64, 0.62, "width", "both")
    fav.write_text(f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">{body}</svg>\n')
    print(f"  {fav.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
