"""Regenerate the extension PNG icons (requires Pillow)."""

from pathlib import Path
from PIL import Image, ImageDraw


ROOT = Path(__file__).parent
SCALE = 8


def draw_icon(size: int) -> None:
    canvas = size * SCALE
    image = Image.new("RGBA", (canvas, canvas), (0, 0, 0, 0))
    pen = ImageDraw.Draw(image)

    def box(left, top, right, bottom):
        return tuple(round(value * canvas) for value in (left, top, right, bottom))

    pen.rounded_rectangle(box(0.03, 0.03, 0.97, 0.97), radius=round(canvas * 0.19), fill="#222536")
    pen.rounded_rectangle(box(0.14, 0.18, 0.86, 0.82), radius=round(canvas * 0.08), outline="#CDD5EF", width=max(1, round(canvas * 0.035)))
    pen.line([box(0.25, 0.70, 0.25, 0.59)[:2], box(0.25, 0.70, 0.25, 0.59)[2:]], fill="#4B71F5", width=max(1, round(canvas * 0.075)))
    pen.line([box(0.43, 0.70, 0.43, 0.46)[:2], box(0.43, 0.70, 0.43, 0.46)[2:]], fill="#4B71F5", width=max(1, round(canvas * 0.075)))
    pen.line([box(0.61, 0.70, 0.61, 0.33)[:2], box(0.61, 0.70, 0.61, 0.33)[2:]], fill="#4B71F5", width=max(1, round(canvas * 0.075)))
    pen.ellipse(box(0.72, 0.33, 0.80, 0.41), fill="#CDD5EF")
    image.resize((size, size), Image.Resampling.LANCZOS).save(ROOT / f"icon-{size}.png")


for icon_size in (16, 32, 48, 128):
    draw_icon(icon_size)
