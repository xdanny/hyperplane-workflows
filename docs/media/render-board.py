# /// script
# requires-python = ">=3.11"
# dependencies = ["Pillow==11.3.0"]
# ///
"""Render the README's synthetic Board diagram; no app or account data is read.

Run: uv run docs/media/render-board.py
Optional: set DIAGRAM_FONT / DIAGRAM_MONO to local TrueType font files.
"""

import math
import os
from pathlib import Path

from PIL import Image, ImageColor, ImageDraw, ImageFont

OUT = Path(__file__).resolve().parent
W, H, SCALE, FPS = 1200, 640, 2, 10
BG = "#10191c"
INK = "#f0f2e8"
MUTED = "#a1b1b3"
LINE = "#35464a"
PANEL = "#182529"
TEAL = "#91dbce"
AMBER = "#efc378"
GREEN = "#b5d79d"


def font_file(env, candidates):
    if os.environ.get(env):
        return os.environ[env]
    for name in candidates:
        try:
            ImageFont.truetype(name, 20)
            return name
        except OSError:
            continue
    raise SystemExit(f"Set {env} to a local TrueType font file")


SANS = font_file("DIAGRAM_FONT", ["/System/Library/Fonts/Avenir Next.ttc", "DejaVuSans.ttf"])
MONO = font_file("DIAGRAM_MONO", ["/System/Library/Fonts/Menlo.ttc", "DejaVuSansMono.ttf"])
FONTS = {}


def render(stage, phase):
    im = Image.new("RGB", (W * SCALE, H * SCALE), BG)
    d = ImageDraw.Draw(im)

    def text(x, y, value, size=22, fill=INK, mono=False):
        key = (size, mono)
        if key not in FONTS:
            FONTS[key] = ImageFont.truetype(MONO if mono else SANS, size * SCALE)
        d.text((x * SCALE, y * SCALE), value, font=FONTS[key], fill=fill)

    def line(points, fill=LINE, width=2):
        d.line([(int(x * SCALE), int(y * SCALE)) for x, y in points], fill=fill, width=width * SCALE)

    def box(bounds, fill=PANEL, outline=LINE, radius=15, width=1):
        d.rounded_rectangle(tuple(int(v * SCALE) for v in bounds), radius * SCALE, fill, outline, width * SCALE)

    def circle(x, y, r, color):
        d.ellipse(((x-r)*SCALE, (y-r)*SCALE, (x+r)*SCALE, (y+r)*SCALE), fill=color)

    def pill(x, y, value, color, width):
        box((x, y, x+width, y+35), fill=BG, outline=color, radius=7)
        text(x+12, y+5, value, 18, color, True)

    text(44, 24, "HYPERPLANE", 19, TEAL, True)
    text(825, 24, "BOARD / ILLUSTRATIVE", 18, MUTED, True)
    text(42, 66, "The worker finishes. You decide.", 44)
    text(44, 128, "One ready ticket per request. Dependent work waits for acceptance.", 23, MUTED)

    labels = [(44, "01", "Saved ticket"), (439, "02", "SDK worker"), (834, "03", "Human review")]
    for x, number, title in labels:
        text(x, 187, number, 17, TEAL, True)
        text(x+35, 181, title, 25)

    active = [TEAL if stage == 0 else LINE, TEAL if stage == 1 else LINE,
              AMBER if stage == 2 else GREEN if stage >= 3 else LINE]
    for x, color in zip((44, 439, 834), active):
        box((x, 230, x+322, 410), outline=color, width=2 if color != LINE else 1)

    text(65, 249, "Parse expenses", 28)
    text(65, 295, "Title + implementation detail", 19, MUTED)
    left_status = ["todo · ready", "in_progress", "review", "done", "done"][stage]
    pill(65, 350, left_status, GREEN if stage >= 3 else AMBER if stage == 2 else TEAL, 168)

    text(460, 249, "Codex SDK", 28)
    text(460, 294, "Edits the selected workspace", 19, MUTED)
    worker_status = ["waiting", "running", "passed", "passed", "passed"][stage]
    text(460, 353, worker_status, 23, TEAL if stage == 1 else MUTED, True)
    if stage == 1:
        for k in range(4):
            height = 9 + 16 * (0.5 + 0.5 * math.sin(phase * math.tau * 3 - k))
            box((681 + k*13, 377-height, 688+k*13, 377), fill=TEAL, outline=TEAL, radius=2)

    text(855, 249, "Inspect the result", 27)
    text(855, 295, "A person marks the ticket done", 18, MUTED)
    review_status = "accepted" if stage >= 3 else "awaiting review" if stage == 2 else "not started"
    pill(855, 350, review_status, GREEN if stage >= 3 else AMBER if stage == 2 else MUTED, 229)

    for xa, xb in ((366, 439), (761, 834)):
        line([(xa+5, 319), (xb-10, 319)], MUTED)
        line([(xb-18, 313), (xb-10, 319), (xb-18, 325)], MUTED)
    if stage in (0, 1):
        start, end = (373, 427) if stage == 0 else (768, 822)
        # A single moving marker shows the handoff without flashing the diagram.
        circle(start+(end-start)*min(phase*1.4, 1), 319, 5, TEAL)

    box((44, 440, 1156, 514), fill=BG, outline=GREEN if stage >= 3 else LINE, radius=11)
    text(65, 455, "NEXT", 16, GREEN if stage >= 3 else MUTED, True)
    text(143, 453, "Render report", 25)
    text(390, 457, "depends on Parse expenses", 20, MUTED)
    text(804, 455, "ready for a later run" if stage >= 3 else "waiting for acceptance", 20,
         GREEN if stage >= 3 else MUTED)

    captions = ["Claim one ready ticket in PostgreSQL.",
                "The worker receives this ticket and its target folder.",
                "Run: passed. Ticket: review. Dependent work still waits.",
                "Human acceptance changes the ticket to done.",
                "The dependent ticket is eligible. It does not start itself."]
    text(44, 541, captions[stage], 23, AMBER if stage == 2 else GREEN if stage >= 3 else INK)
    text(44, 591, "Board path: no host acceptance check / no automatic repair", 17, MUTED, True)
    for k in range(5):
        box((1040+k*24, 600, 1054+k*24, 604), TEAL if k == stage else LINE,
            TEAL if k == stage else LINE, radius=2)
    return im.resize((W, H), Image.Resampling.LANCZOS)


if __name__ == "__main__":
    # Hold review longest: the pause is the point of this execution path.
    durations = [2.5, 3.5, 5, 2.5, 4]
    frames = [render(stage, n / max(1, int(seconds*FPS)-1))
              for stage, seconds in enumerate(durations)
              for n in range(int(seconds*FPS))]
    samples = Image.new("RGB", (600, 320 * 5))
    for stage in range(5):
        samples.paste(render(stage, 0.5).resize((600, 320)), (0, stage*320))
    # Reserve every semantic color so a long review hold cannot erase the
    # less frequent running/accepted colors during global GIF quantization.
    exact = [v for color in (BG, INK, MUTED, LINE, PANEL, TEAL, AMBER, GREEN)
             for v in ImageColor.getrgb(color)]
    palette = Image.new("P", (1, 1))
    palette.putpalette(exact + samples.quantize(colors=248).getpalette()[:248*3])
    frames = [frame.quantize(palette=palette, dither=Image.Dither.NONE) for frame in frames]
    frames[0].save(OUT / "board-flow.gif", save_all=True, append_images=frames[1:],
                   duration=1000//FPS, loop=0, optimize=True, disposal=1)
    render(2, 0.5).save(OUT / "board-flow.png", optimize=True)
    print(f"Rendered {len(frames)} frames, {sum(durations):g}s, {W}x{H}")
