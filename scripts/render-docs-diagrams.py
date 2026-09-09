#!/usr/bin/env python3
"""Render the documentation architecture diagrams to SVG and PNG.

Requires Pillow. Run from any directory with python3 scripts/render-docs-diagrams.py.
SVG is editable source alongside this file. PNG works in Registry Markdown without
requiring a Mermaid renderer. make docs copies both from templates/assets.
"""

from html import escape
from math import hypot
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

    def centered_text(self, cx, y, value, size=16, bold=False, color=INK):
        width = self.draw.textlength(value, font=font(size, bold))/2
        self.text(cx-width/2, y, value, size, bold, color)

    def motherduck_mark(self, x, y, size=38):
        # Official mark from motherduck-docs/static/img/icons/brands/duckfeet_orange.svg.
        # Keep vector paths in SVG output and use the checked-in raster for PNG.
        with Image.open(OUT / "motherduck-mark.png") as source:
            mark = source.convert("RGBA").resize((size*2, size*2), Image.Resampling.LANCZOS)
            self.image.paste(mark, (x*2, y*2), mark)
        source = (OUT / "motherduck-mark.svg").read_text()
        paths = source[source.index(">")+1:source.rindex("</svg>")]
        self.svg.append(f'<g transform="translate({x} {y}) scale({size/25})">{paths}</g>')

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

    def line(self, points, color=INK, width=2):
        self.draw.line([(x*2, y*2) for x, y in points], fill=color, width=width*2)
        coords = " ".join(f"{x},{y}" for x, y in points)
        self.svg.append(f'<polyline points="{coords}" fill="none" stroke="{color}" stroke-width="{width}"/>')

    def dashed(self, points, color="#6B46A3"):
        for (x1, y1), (x2, y2) in zip(points, points[1:]):
            length = hypot(x2-x1, y2-y1)
            for start in range(0, int(length), 12):
                end = min(start+6, length)
                self.line([(x1+(x2-x1)*start/length, y1+(y2-y1)*start/length),
                           (x1+(x2-x1)*end/length, y1+(y2-y1)*end/length)], color)

    def ellipse(self, x, y, w, h, fill=WHITE, stroke=INK):
        self.draw.ellipse((x*2, y*2, (x+w)*2, (y+h)*2), fill=fill, outline=stroke, width=2)
        self.svg.append(f'<ellipse cx="{x+w/2}" cy="{y+h/2}" rx="{w/2}" ry="{h/2}" fill="{fill}" stroke="{stroke}"/>')

    def chip(self, x, y, w=112, h=80, fill=GOLD):
        self.rect(x, y, w, h, fill, radius=4)
        core_color = "#A4D7C9" if fill == TEAL else "#97D4FF"
        self.rect(x+15, y+15, w-30, h-30, core_color, radius=2)
        self.rect(x+w/2-10, y+h/2-10, 20, 20, WHITE, radius=1)
        for pin in range(4):
            px = x+20+pin*(w-40)/3
            self.line([(px, y-7), (px, y)])
            self.line([(px, y+h), (px, y+h+7)])
        for pin in range(3):
            py = y+18+pin*(h-36)/2
            self.line([(x-7, py), (x, py)])
            self.line([(x+w, py), (x+w+7, py)])

    def database(self, x, y, w=132, h=100):
        self.ellipse(x, y+h-26, w, 26, GOLD)
        self.rect(x, y+13, w, h-26, GOLD, stroke=GOLD, radius=0)
        self.line([(x, y+13), (x, y+h-13)], width=1)
        self.line([(x+w, y+13), (x+w, y+h-13)], width=1)
        self.ellipse(x, y, w, 26, "#FFF9BF")
        self.line([(x+19, y+45), (x+w-19, y+45)], color="#B8A65C", width=1)
        self.line([(x+19, y+64), (x+w-19, y+64)], color="#B8A65C", width=1)

    def save(self, name):
        OUT.mkdir(parents=True, exist_ok=True)
        (OUT / f"{name}.svg").write_text("\n".join(self.svg + ["</svg>"]) + "\n")
        self.image.save(OUT / f"{name}.png", optimize=True)


def brand(d, title, detail):
    d.motherduck_mark(32, 26)
    d.text(83, 28, "MotherDuck", 27, bold=True)
    d.text(285, 33, title, 22)
    d.text(34, 83, detail, 15, color=MUTED)


def account(d, x, y, w, h, label, reader=False):
    d.rect(x, y, w, h, "#F0F8FD" if reader else SAND,
           stroke="#AACADD" if reader else "#C5BBB1", radius=5)
    d.ellipse(x+20, y+19, 13, 13, INK)
    d.line([(x+16, y+44), (x+16, y+40), (x+22, y+36),
            (x+31, y+36), (x+37, y+40), (x+37, y+44)])
    d.text(x+48, y+20, label, 20, bold=True)


def share(d, x, y):
    d.rect(x, y, 108, 58, BLUE, radius=4)
    d.line([(x+28, y+29), (x+82, y+17)], width=1)
    d.line([(x+28, y+29), (x+82, y+41)], width=1)
    for dx, dy in [(18, 22), (76, 10), (76, 34)]:
        d.ellipse(x+dx, y+dy, 14, 14, WHITE)


def pool(d, x, y):
    d.chip(x+27, y, 96, 66, BLUE)
    d.chip(x, y+27, 96, 66, BLUE)


def code(d, x, y, label, tint="#E9E2F7"):
    d.rect(x+9, y+9, 112, 86, tint, radius=4)
    d.rect(x, y, 112, 86, WHITE, radius=4)
    d.rect(x, y, 112, 23, tint, radius=4)
    d.text(x+12, y+4, label, 12)
    d.centered_text(x+56, y+30, "{ }", 32, bold=True, color="#60428C")


def app(d, x, y):
    d.rect(x, y, 108, 82, WHITE, radius=4)
    d.line([(x, y+18), (x+108, y+18)], width=1)
    for dx in [10, 19, 28]:
        d.ellipse(x+dx, y+7, 3, 3, INK)
    for dx, h in [(18, 19), (38, 36), (58, 27), (78, 44)]:
        d.rect(x+dx, y+71-h, 11, h, "#97D4FF", stroke="#97D4FF")


def deployment():
    d = Diagram(1200, 480, "Deployment model",
                "Admin Terraform provisions service accounts and compute. Writer-scoped Terraform "
                "creates writer-owned databases and restricted shares. A separate pipeline loads data. "
                "Reader accounts attach shares and query on independent read pools.")
    brand(d, "Deployment model", "Provision infrastructure, then load and query data")
    code(d, 40, 134, "admin.tf")
    d.centered_text(100, 242, "Admin setup", 18, True)
    d.dashed([(164, 179), (1043, 179)])
    d.text(191, 151, "Accounts + compute", 15, color="#60428C")
    for x in [505, 1043]:
        d.dashed([(x, 179), (x, 204)])
        d.arrow([(x, 204), (x, 220)], "#6B46A3")
    account(d, 258, 220, 554, 232, "Writer account")
    account(d, 854, 220, 314, 232, "Reader account", True)
    d.rect(36, 320, 132, 60, WHITE, radius=4)
    d.line([(57, 350), (148, 350)], "#068475")
    for x in [53, 94, 135]:
        d.rect(x, 340, 20, 20, TEAL, stroke="#068475", radius=3)
    d.centered_text(102, 412, "Pipeline", 18, True)
    d.chip(290, 310, 98, 76, TEAL)
    d.centered_text(339, 412, "R/W Duckling", 17, True)
    d.database(444, 297, 116, 100)
    d.centered_text(502, 412, "Database", 18, True)
    share(d, 651, 321)
    d.centered_text(705, 412, "Restricted share", 17, True)
    pool(d, 941, 298)
    d.centered_text(1003, 412, "Read pool", 18, True)
    for a, b in [(169, 280), (396, 437), (568, 643), (767, 928)]:
        d.arrow([(a, 350), (b, 350)])
    d.text(441, 267, "Writer Terraform manages data + access", 14, color=MUTED)
    d.save("deployment-model")


def warehouse():
    d = Diagram(1200, 560, "Layered warehouse",
                "One writer account owns raw, transform, and marts databases. Raw orders feed the "
                "orders_latest view, then a pipeline refreshes the daily_revenue physical table. "
                "BI uses the same writer identity's read pool, which can access all three layers.")
    brand(d, "Layered warehouse", "Pipeline-managed refreshes · One writer identity across all layers")
    account(d, 32, 124, 922, 404, "Writer account")
    for x, name, obj in [(90, "Raw", "orders · table"),
                          (370, "Transform", "orders_latest · view"),
                          (650, "Marts", "daily_revenue · table")]:
        d.database(x, 209, 132, 100)
        d.centered_text(x+66, 325, name, 21, True)
        d.centered_text(x+66, 357, obj, 16, color=MUTED)
    for a, b, label in [(230, 362, "dedupe"), (510, 642, "refresh")]:
        d.arrow([(a, 258), (b, 258)])
        d.centered_text((a+b)/2, 228, label, 14, color=MUTED)
    # A common read path makes access to all layers explicit.
    d.line([(156, 391), (156, 426), (717, 426)], "#5796BD")
    for x in [436, 716]:
        d.line([(x, 391), (x, 426)], "#5796BD")
    d.arrow([(717, 426), (775, 426)], "#5796BD")
    pool(d, 789, 393)
    d.text(86, 467, "Read access to every layer", 17, color=MUTED)
    d.centered_text(850, 498, "Read pool", 16, True)
    app(d, 1034, 388)
    d.arrow([(920, 430), (1026, 430)])
    d.centered_text(1088, 495, "BI", 19, True)
    d.save("layered-warehouse")


def customers():
    d = Diagram(1200, 610, "Customer-facing analytics",
                "A central writer owns both tenant databases and restricted shares. Each share is "
                "granted only to its tenant reader account with separate compute. The application "
                "backend authenticates the tenant and routes queries to the matching reader.")
    brand(d, "Customer-facing analytics", "Writer-owned data · Tenant-specific access and compute")
    account(d, 32, 125, 535, 450, "Central writer account")
    for y, tenant in [(194, "Acme"), (395, "Globex")]:
        d.database(80, y, 128, 100)
        d.centered_text(144, y+118, tenant+" database", 18, True)
        share(d, 373, y+24)
        d.centered_text(427, y+118, "Restricted share", 18, True)
        d.arrow([(216, y+54), (365, y+54)])
        account(d, 615, y-43, 281, 186, tenant+" reader", True)
        pool(d, 697, y+22)
        d.arrow([(489, y+54), (684, y+54)])
    d.line([(56, 365), (543, 365)], "#D8CFC5", 1)
    app(d, 1028, 298)
    d.centered_text(1082, 402, "Backend", 20, True)
    d.centered_text(1082, 435, "Auth + routing", 15, color=MUTED)
    # Query arrows point from the backend to the selected tenant's pool.
    d.arrow([(1020, 323), (959, 323), (959, 248), (842, 248)])
    d.arrow([(1020, 360), (959, 360), (959, 449), (842, 449)])
    d.text(943, 211, "queries", 14, color=MUTED)
    d.save("customer-facing-analytics")


def promotion():
    d = Diagram(1200, 470, "Blueprints code promotion",
                "With optional staging enabled, pull requests deploy previews and main deploys staging "
                "under the same staging account. A published release deploys its exact tag under the "
                "separate production account. Terraform provisions identities first. Arrows promote code, "
                "not database contents.")
    brand(d, "Blueprints promotion", "Code promotion with staging enabled · Terraform provisions the accounts first")
    for x, label, title in [(93, "pull request", "PR preview"),
                             (447, "main", "Staging"),
                             (948, "release tag", "Production")]:
        code(d, x, 130, label)
        d.centered_text(x+56, 241, title, 20, True)
    d.arrow([(220, 175), (428, 175)])
    d.centered_text(324, 142, "merge", 15, color=MUTED)
    d.arrow([(574, 175), (928, 175)])
    d.centered_text(751, 142, "publish release", 15, color=MUTED)
    account(d, 32, 300, 657, 142, "Staging account")
    account(d, 752, 300, 416, 142, "Production account", True)
    for x in [149, 503, 1004]:
        d.dashed([(x, 276), (x, 287)])
        d.arrow([(x, 287), (x, 300)], "#6B46A3")
    d.rect(81, 365, 216, 48, TEAL, stroke="#8CBEB3", radius=4)
    d.centered_text(189, 377, "Preview resources", 17, True)
    d.rect(402, 365, 234, 48, TEAL, stroke="#8CBEB3", radius=4)
    d.centered_text(519, 377, "Staging resources", 17, True)
    d.rect(810, 365, 300, 48, BLUE, stroke="#AACADD", radius=4)
    d.centered_text(960, 377, "Exact release tag", 17, True)
    d.save("blueprints-promotion")


def readme():
    """Separate infrastructure provisioning from runtime data flow.

    Account boundaries follow motherduck-docs/concepts/resource-management.md.
    Writer-owned data is published to an independent reader account.
    """
    d = Diagram(1320, 464, "MotherDuck infrastructure with Terraform",
                "Terraform manages accounts, compute, data, and access. A pipeline writes through "
                "the writer's Duckling to its database. A read-only share publishes data to "
                "a separate reader account and read pool for BI and applications.")

    d.rect(264, 24, 896, 416, WHITE, stroke="#C5BBB1", radius=8)
    d.motherduck_mark(290, 42)
    d.text(340, 44, "MotherDuck", 28, bold=True)
    d.text(818, 56, "Accounts · Compute · Data · Access", 16, color=MUTED)
    d.line([(290, 95), (1134, 95)], color="#E1D6CB", width=1)

    d.rect(62, 54, 142, 124, "#E9E2F7", stroke="#6B46A3", radius=5)
    d.rect(50, 42, 142, 124, WHITE, stroke="#6B46A3", radius=5)
    d.rect(50, 42, 142, 25, "#E9E2F7", stroke="#6B46A3", radius=5)
    d.text(67, 48, "main.tf", 13, color="#60428C")
    d.centered_text(121, 79, "{ }", 43, bold=True, color="#60428C")
    d.centered_text(127, 189, "Terraform", 22, bold=True)

    # Dashed provisioning branches terminate at the account boundaries.
    d.text(295, 108, "provisions", 14, color="#60428C")
    d.dashed([(205, 132), (1006, 132)])
    for x in [568, 1006]:
        d.dashed([(x, 132), (x, 163)])
        d.arrow([(x, 163), (x, 180)], color="#6B46A3")

    d.rect(290, 180, 556, 236, SAND, stroke="#C5BBB1", radius=4)
    d.rect(878, 180, 256, 236, "#F0F8FD", stroke="#AACADD", radius=4)
    for x, title in [(312, "Writer account"), (900, "Reader account")]:
        d.ellipse(x, 197, 14, 14, fill=INK)
        d.draw.arc(((x-4)*2, 214*2, (x+18)*2, 234*2), 180, 360, fill=INK, width=4)
        d.svg.append(f'<path d="M {x-4} 224 A 11 10 0 0 1 {x+18} 224" fill="none" stroke="{INK}" stroke-width="2"/>')
        d.text(x+30, 197, title, 20, bold=True)

    d.rect(52, 277, 132, 66, WHITE, stroke=INK, radius=4)
    d.line([(76, 310), (160, 310)], color="#068475")
    for x, fill in [(66, WHITE), (108, TEAL), (150, "#50C7B7")]:
        d.rect(x, 300, 20, 20, fill, stroke="#068475", radius=3)
    d.centered_text(118, 376, "Pipeline", 18, bold=True)

    d.chip(318, 270, fill=TEAL)
    d.centered_text(374, 376, "R/W Duckling", 18, bold=True)
    d.database(486, 260)
    d.centered_text(552, 376, "Database", 18, bold=True)

    d.rect(691, 281, 116, 58, BLUE, radius=4)
    d.ellipse(708, 300, 15, 15, WHITE)
    d.ellipse(774, 289, 15, 15, WHITE)
    d.ellipse(774, 313, 15, 15, WHITE)
    d.line([(723, 307), (774, 296)], width=1)
    d.line([(723, 307), (774, 320)], width=1)
    d.centered_text(749, 376, "Read-only share", 18, bold=True)

    d.chip(976, 254, w=112, h=74, fill=BLUE)
    d.chip(936, 288, w=112, h=74, fill=BLUE)
    d.centered_text(1012, 376, "Read pool", 18, bold=True)

    d.rect(1190, 267, 98, 82, WHITE, radius=4)
    d.line([(1190, 284), (1288, 284)], width=1)
    for x in [1200, 1209, 1218]:
        d.ellipse(x, 274, 3, 3, fill=INK)
    for x, h in [(1207, 20), (1226, 34), (1245, 26), (1264, 43)]:
        d.rect(x, 339-h, 10, h, "#97D4FF", stroke="#97D4FF", radius=1)
    d.centered_text(1239, 376, "BI & apps", 18, bold=True)

    for a, b in [(184, 309), (437, 480), (619, 684), (808, 929), (1095, 1183)]:
        d.arrow([(a, 310), (b, 310)])
    d.save("readme-architecture")


if __name__ == "__main__":
    deployment()
    warehouse()
    customers()
    promotion()
    readme()
