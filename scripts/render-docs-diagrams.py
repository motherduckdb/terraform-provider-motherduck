#!/usr/bin/env python3
"""Render the documentation architecture diagrams to SVG and PNG.

Requires Pillow. Run from any directory with python3 scripts/render-docs-diagrams.py.
SVG is editable source alongside this file. PNG works in Registry Markdown without
requiring a Mermaid renderer. make docs copies both from templates/assets.
"""

from html import escape
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont


OUT = Path(__file__).resolve().parents[1] / "templates" / "assets"
# MotherDuck website and docs palette, checked September 2026.
# https://motherduck.com/docs/ exposes --md-color-* tokens.
INK = "#383838"       # black
MUTED = "#666666"
LINE = "#383838"
PAPER = "#F8F8F7"     # snow
SAND = "#F4EFEA"      # sand
TEAL = "#D0EEE8"      # restrained tint of garden (#16AA98)
BLUE = "#CEEBFF"      # website sky tint, with light-sky (#97D4FF) accents
GOLD = "#FAF175"      # website pale yellow
WHITE = "#FFFFFF"


def font(size, bold=False):
    names = (
        ["/System/Library/Fonts/Supplemental/Arial Bold.ttf",
         "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"]
        if bold else
        ["/System/Library/Fonts/Supplemental/Arial.ttf",
         "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"]
    )
    for name in names:
        if Path(name).exists():
            return ImageFont.truetype(name, size * 2)
    return ImageFont.load_default(size=size * 2)


class Diagram:
    def __init__(self, width, height, title, subtitle):
        self.width, self.height = width, height
        self.image = Image.new("RGB", (width * 2, height * 2), PAPER)
        self.draw = ImageDraw.Draw(self.image)
        self.svg = [
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
            f'viewBox="0 0 {width} {height}" role="img" aria-labelledby="title desc">',
            f'<title id="title">{escape(title)}</title><desc id="desc">{escape(subtitle)}</desc>',
            f'<rect width="{width}" height="{height}" fill="{PAPER}"/>',
        ]

    def text(self, x, y, value, size=16, bold=False, color=INK):
        face = font(size, bold)
        bounds = self.draw.textbbox((x * 2, y * 2), value, font=face)
        if bounds[2] > (self.width - 20) * 2:
            raise ValueError(f"Text exceeds diagram width: {value}")
        self.draw.text((x * 2, y * 2), value, font=face, fill=color)
        weight = "700" if bold else "400"
        self.svg.append(
            f'<text x="{x}" y="{y + size}" fill="{color}" font-family="Arial, sans-serif" '
            f'font-size="{size}" font-weight="{weight}">{escape(value)}</text>'
        )

    def rect(self, x, y, w, h, fill=WHITE, stroke=INK, radius=2):
        self.draw.rounded_rectangle((x*2, y*2, (x+w)*2, (y+h)*2), radius=radius*2,
                                    fill=fill, outline=stroke, width=2)
        self.svg.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" '
                        f'rx="{radius}" fill="{fill}" stroke="{stroke}"/>')

    def card(self, x, y, w, h, title, subtitle="", fill=WHITE):
        for value, size, bold in [(title, 20, True), (subtitle, 14, False)]:
            if self.draw.textlength(value, font=font(size, bold)) > (w-32)*2:
                raise ValueError(f"Text exceeds card width: {value}")
        self.rect(x, y, w, h, fill)
        self.text(x+16, y+(12 if subtitle else (h-24)/2), title, 20, bold=True)
        if subtitle:
            self.text(x+16, y+40, subtitle, 14, color=INK)

    def group(self, x, y, w, h, label):
        self.rect(x, y, w, h, fill=SAND, stroke="#C5BBB1")
        self.text(x+18, y+12, label, 16, bold=True)

    def arrow(self, points, color=LINE):
        self.draw.line([(x*2, y*2) for x, y in points], fill=color, width=4)
        x, y = points[-1]
        px, py = points[-2]
        if x > px:
            head = [(x, y), (x-8, y-5), (x-8, y+5)]
        elif x < px:
            head = [(x, y), (x+8, y-5), (x+8, y+5)]
        elif y > py:
            head = [(x, y), (x-5, y-8), (x+5, y-8)]
        else:
            head = [(x, y), (x-5, y+8), (x+5, y+8)]
        self.draw.polygon([(x*2, y*2) for x, y in head], fill=color)
        coords = " ".join(f"{x},{y}" for x, y in points)
        self.svg.append(f'<polyline points="{coords}" fill="none" stroke="{color}" stroke-width="2"/>')
        coords = " ".join(f"{x},{y}" for x, y in head)
        self.svg.append(f'<polygon points="{coords}" fill="{color}"/>')

    def save(self, name):
        OUT.mkdir(parents=True, exist_ok=True)
        (OUT / f"{name}.svg").write_text("\n".join(self.svg + ["</svg>"]) + "\n")
        self.image.save(OUT / f"{name}.png", optimize=True)


def deployment():
    d = Diagram(1080, 350, "Deployment",
                "An admin provisions accounts. The writer owns databases, a pipeline writes data, "
                "and readers attach restricted shares and query on separate compute.")
    d.card(32, 38, 252, 72, "Admin setup", "Accounts and compute", GOLD)
    d.card(414, 38, 252, 72, "Writer account", "Terraform", TEAL)
    d.card(796, 38, 252, 72, "Reader account", "BI or application", BLUE)
    d.arrow([(284, 74), (414, 74)])
    d.text(315, 47, "token", 14, color=MUTED)
    d.card(32, 238, 252, 72, "Pipeline", fill=TEAL)
    d.card(414, 238, 252, 72, "Databases")
    d.card(796, 238, 252, 72, "Restricted shares", fill=BLUE)
    d.arrow([(540, 110), (540, 238)])
    d.text(554, 163, "owns", 14, color=MUTED)
    d.arrow([(284, 274), (414, 274)])
    d.text(311, 247, "writes", 14, color=MUTED)
    d.arrow([(666, 274), (796, 274)])
    d.text(696, 247, "publishes", 14, color=MUTED)
    d.arrow([(922, 238), (922, 110)])
    d.text(936, 155, "attach", 14, color=MUTED)
    d.text(936, 176, "then read", 14, color=MUTED)
    d.save("deployment-model")


def warehouse():
    d = Diagram(1080, 318, "Layered warehouse",
                "One writer owns raw, transform, and marts databases. Its BI read pool can read "
                "all three layers. The pipeline refreshes rows outside Terraform.")
    d.group(32, 24, 1016, 160, "Writer account")
    d.card(52, 80, 242, 74, "Raw", "orders")
    d.card(419, 80, 242, 74, "Transform", "orders_latest")
    d.card(786, 80, 242, 74, "Marts", "daily_revenue")
    d.arrow([(294, 117), (419, 117)])
    d.text(324, 90, "dedupe", 14, color=MUTED)
    d.arrow([(661, 117), (786, 117)])
    d.text(693, 90, "refresh", 14, color=MUTED)
    d.card(786, 232, 242, 62, "BI", "Writer's read pool", BLUE)
    d.arrow([(907, 154), (907, 232)])
    d.save("layered-warehouse")


def customers():
    d = Diagram(1120, 382, "Customer isolation",
                "A central writer owns Acme and Globex databases. Each restricted share is granted "
                "to its own reader account. The backend authenticates and routes each tenant.")
    d.group(242, 24, 650, 144, "Acme")
    d.group(242, 214, 650, 144, "Globex")
    d.card(32, 155, 160, 72, "Writer", fill=TEAL)
    for y in [80, 270]:
        d.card(260, y, 162, 64, "Database")
        d.card(461, y, 186, 64, "Share", "Restricted")
        d.card(686, y, 186, 64, "Reader account", "Read pool", BLUE)
        d.arrow([(422, y+32), (461, y+32)])
        d.arrow([(647, y+32), (686, y+32)])
    d.card(948, 155, 140, 72, "Backend", "Auth + routing", GOLD)
    d.arrow([(192, 178), (215, 178), (215, 112), (260, 112)])
    d.arrow([(192, 204), (215, 204), (215, 302), (260, 302)])
    d.arrow([(872, 112), (919, 112), (919, 178), (948, 178)])
    d.arrow([(872, 302), (919, 302), (919, 204), (948, 204)])
    d.save("customer-facing-analytics")


def promotion():
    d = Diagram(1080, 226, "Blueprints promotion",
                "With optional staging enabled, pull requests deploy previews and main deploys staging "
                "under the staging account. A release deploys its exact tag under the production account. "
                "Terraform provisions the identities first. Code is promoted, not data.")
    d.group(32, 32, 658, 162, "Staging account")
    d.group(738, 32, 310, 162, "Production account")
    d.card(52, 99, 244, 66, "Preview", "Pull request", TEAL)
    d.card(424, 99, 244, 66, "Staging", "Main", TEAL)
    d.card(758, 99, 270, 66, "Production", "Release tag", BLUE)
    d.arrow([(296, 132), (424, 132)])
    d.text(337, 105, "merge", 14, color=MUTED)
    d.arrow([(668, 132), (758, 132)])
    d.text(684, 105, "release", 14, color=MUTED)
    d.save("blueprints-promotion")


if __name__ == "__main__":
    deployment()
    warehouse()
    customers()
    promotion()
