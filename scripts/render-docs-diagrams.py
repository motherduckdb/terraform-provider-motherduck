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
PURPLE = "#6B46A3"    # Terraform provisioning
PURPLE_TEXT = "#60428C"
ACCESS = "#3F7FA8"    # read access paths
READER_FILL = "#F0F8FD"
READER_STROKE = "#AACADD"
SAND_STROKE = "#C5BBB1"


FONT_FAMILY = "Inter, Arial, sans-serif"


def font(size, bold=False):
    # Inter matches the MotherDuck site. Arial and DejaVu are fallbacks.
    names = (
        ["/usr/share/fonts/opentype/inter/Inter-SemiBold.otf",
         str(Path.home() / "Library/Fonts/Inter-SemiBold.otf"),
         "/System/Library/Fonts/Supplemental/Arial Bold.ttf",
         "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"]
        if bold else
        ["/usr/share/fonts/opentype/inter/Inter-Regular.otf",
         str(Path.home() / "Library/Fonts/Inter-Regular.otf"),
         "/System/Library/Fonts/Supplemental/Arial.ttf",
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
        weight = "600" if bold else "400"
        self.svg.append(
            f'<text x="{x}" y="{y + size}" fill="{color}" font-family="{FONT_FAMILY}" '
            f'font-size="{size}" font-weight="{weight}">{escape(value)}</text>'
        )

    def centered_text(self, cx, y, value, size=16, bold=False, color=INK):
        self.text(cx-self.text_width(value, size, bold)/2, y, value, size, bold, color)

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
                        f'rx="{radius}" fill="{fill}" stroke="{stroke or 'none'}"/>')

    def text_width(self, value, size=16, bold=False):
        return self.draw.textlength(value, font=font(size, bold)) / 2

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

    def dashed(self, points, color=PURPLE):
        for (x1, y1), (x2, y2) in zip(points, points[1:]):
            length = hypot(x2-x1, y2-y1)
            for start in range(0, int(length), 12):
                end = min(start+6, length)
                self.line([(x1+(x2-x1)*start/length, y1+(y2-y1)*start/length),
                           (x1+(x2-x1)*end/length, y1+(y2-y1)*end/length)], color)

    def ellipse(self, x, y, w, h, fill=WHITE, stroke=INK):
        self.draw.ellipse((x*2, y*2, (x+w)*2, (y+h)*2), fill=fill, outline=stroke, width=2)
        self.svg.append(f'<ellipse cx="{x+w/2}" cy="{y+h/2}" rx="{w/2}" ry="{h/2}" fill="{fill}" stroke="{stroke}"/>')

    def chip(self, x, y, w=112, h=80, fill=GOLD, pins=True):
        """Compute icon: a rounded package with a core and short pins."""
        stroke = "#5E9C91" if fill == TEAL else "#6E9FBF"
        core = "#A4D7C9" if fill == TEAL else "#97D4FF"
        if pins:
            for pin in range(3):
                px = x+w*(pin+1)/4
                self.line([(px, y-6), (px, y)], stroke, 1)
                self.line([(px, y+h), (px, y+h+6)], stroke, 1)
                py = y+h*(pin+1)/4
                self.line([(x-6, py), (x, py)], stroke, 1)
                self.line([(x+w, py), (x+w+6, py)], stroke, 1)
        self.rect(x, y, w, h, fill, stroke=stroke, radius=6)
        self.rect(x+w*0.28, y+h*0.26, w*0.44, h*0.48, core, stroke=stroke, radius=3)

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


# Every diagram uses the same visual grammar. Dashed purple lines are Terraform
# provisioning. Solid dark arrows are data or code movement. Blue lines are read
# access. Every arrow carries a short verb so the picture reads without the text.


def brand(d, title, detail):
    d.motherduck_mark(32, 30, 30)
    d.text(72, 30, title, 24, bold=True)
    d.text(34, 74, detail, 15, color=MUTED)


def legend(d, x, y, access=False, data_label="Data flow", terraform=True):
    """Draw the shared legend with its left edge at x."""
    if terraform:
        d.dashed([(x, y+9), (x+34, y+9)])
        d.text(x+42, y, "Terraform provisions", 13, color=PURPLE_TEXT)
        x += 42 + d.text_width("Terraform provisions", 13) + 26
    d.arrow([(x, y+9), (x+34, y+9)])
    d.text(x+42, y, data_label, 13, color=MUTED)
    if access:
        x += 42 + d.text_width(data_label, 13) + 26
        d.line([(x, y+9), (x+34, y+9)], ACCESS)
        d.text(x+42, y, "Read access", 13, color=ACCESS)


def legend_width(d, access=False, data_label="Data flow", terraform=True):
    width = 42 + d.text_width(data_label, 13)
    if terraform:
        width += 42 + d.text_width("Terraform provisions", 13) + 26
    if access:
        width += 26 + 42 + d.text_width("Read access", 13)
    return width


def label(d, cx, y, value, bg=PAPER, color=MUTED, size=13):
    """Centered arrow label on a background pill so it never collides with lines."""
    width = d.text_width(value, size)
    d.rect(cx-width/2-5, y-2, width+10, size+8, bg, stroke=None, radius=3)
    d.centered_text(cx, y, value, size, color=color)


def step(d, cx, cy, number):
    """Numbered badge that matches a numbered list in the guide."""
    d.ellipse(cx-11, cy-11, 22, 22, PURPLE, stroke=PURPLE)
    d.centered_text(cx, cy-9, str(number), 13, True, WHITE)


def flow(d, x1, x2, y, verb="", bg=PAPER, color=LINE):
    d.arrow([(x1, y), (x2, y)], color)
    if verb:
        label(d, (x1+x2)/2, y-26, verb, bg)


def account(d, x, y, w, h, label_text, reader=False, note=""):
    d.rect(x, y, w, h, READER_FILL if reader else SAND,
           stroke=READER_STROKE if reader else SAND_STROKE, radius=5)
    d.ellipse(x+20, y+19, 13, 13, INK)
    d.line([(x+16, y+44), (x+16, y+40), (x+22, y+36),
            (x+31, y+36), (x+37, y+40), (x+37, y+44)])
    d.text(x+48, y+20, label_text, 20, bold=True)
    if note:
        d.text(x+48+d.text_width(label_text, 20, True)+12, y+24, note, 14, color=MUTED)


def share(d, x, y):
    d.rect(x, y, 108, 58, BLUE, radius=4)
    d.line([(x+28, y+29), (x+82, y+17)], width=1)
    d.line([(x+28, y+29), (x+82, y+41)], width=1)
    for dx, dy in [(18, 22), (76, 10), (76, 34)]:
        d.ellipse(x+dx, y+dy, 14, 14, WHITE)


def pool(d, x, y):
    """Read-scaling pool: two stacked Ducklings, only the front one detailed."""
    d.rect(x+24, y, 96, 66, BLUE, stroke="#6E9FBF", radius=6)
    d.rect(x+12, y+12, 96, 66, BLUE, stroke="#6E9FBF", radius=6)
    d.chip(x, y+24, 96, 66, BLUE, pins=False)


def code(d, x, y, tab, tint="#E9E2F7", w=112):
    d.rect(x+9, y+9, w, 86, tint, stroke=PURPLE, radius=4)
    d.rect(x, y, w, 86, WHITE, stroke=PURPLE, radius=4)
    d.rect(x, y, w, 23, tint, stroke=PURPLE, radius=4)
    d.text(x+10, y+4, tab, 12, color=PURPLE_TEXT)
    d.centered_text(x+w/2, y+30, "{ }", 32, bold=True, color=PURPLE_TEXT)


def pipeline(d, x, y):
    d.rect(x, y, 132, 60, WHITE, radius=4)
    d.line([(x+21, y+30), (x+112, y+30)], "#068475")
    for dx, fill in [(17, WHITE), (58, TEAL), (99, "#50C7B7")]:
        d.rect(x+dx, y+20, 20, 20, fill, stroke="#068475", radius=3)


def app(d, x, y):
    d.rect(x, y, 108, 82, WHITE, radius=4)
    d.line([(x, y+18), (x+108, y+18)], width=1)
    for dx in [10, 19, 28]:
        d.ellipse(x+dx, y+7, 3, 3, INK)
    for dx, h in [(18, 19), (38, 36), (58, 27), (78, 44)]:
        d.rect(x+dx, y+71-h, 11, h, "#97D4FF", stroke="#97D4FF")


def caption(d, cx, y, title, detail=""):
    d.centered_text(cx, y, title, 18, True)
    if detail:
        d.centered_text(cx, y+26, detail, 14, color=MUTED)


def readme():
    """Hero diagram. Separate provisioning from runtime data flow.

    Account boundaries follow motherduck-docs/concepts/resource-management.md.
    Writer-owned data is published to an independent reader account.
    """
    d = Diagram(1440, 500, "MotherDuck infrastructure with Terraform",
                "An admin root and a writer root provision accounts, compute, databases, shares, "
                "and grants. A pipeline loads data through the writer's Duckling. A restricted "
                "share is granted to a separate reader account, attached once, and queried "
                "through that reader's read pool by BI and applications.")

    d.rect(262, 24, 1000, 452, WHITE, stroke=SAND_STROKE, radius=8)
    d.motherduck_mark(288, 42)
    d.text(338, 44, "MotherDuck", 28, bold=True)
    legend(d, 1236-legend_width(d), 58)
    d.line([(288, 95), (1236, 95)], color="#E1D6CB", width=1)

    code(d, 46, 48, "admin.tf + writer.tf", w=150)
    caption(d, 126, 160, "Terraform", "admin and writer roots")

    # Dashed provisioning branches terminate at the account boundaries.
    d.dashed([(205, 128), (1128, 128)])
    for x in [586, 1128]:
        d.dashed([(x, 128), (x, 162)])
        d.arrow([(x, 162), (x, 184)], color=PURPLE)
    label(d, 440, 114, "accounts, compute, databases, shares, grants", WHITE, PURPLE_TEXT)

    account(d, 288, 184, 612, 262, "Writer account", note="owns the data")
    account(d, 954, 184, 282, 262, "Reader account", True, note="read only")

    pipeline(d, 60, 316)
    caption(d, 126, 398, "Pipeline")

    d.chip(330, 308, fill=TEAL)
    caption(d, 386, 398, "R/W Duckling")
    d.database(540, 297)
    caption(d, 606, 398, "Database")
    share(d, 764, 318)
    caption(d, 818, 398, "Restricted share")

    pool(d, 1020, 294)
    caption(d, 1095, 398, "Read pool")

    app(d, 1300, 305)
    caption(d, 1354, 398, "BI & apps")

    flow(d, 194, 318, 346, "loads")
    flow(d, 451, 530, 346, "writes", SAND)
    flow(d, 675, 756, 346, "publishes", SAND)
    flow(d, 876, 1004, 346, "grant + attach")
    d.arrow([(1292, 346), (1154, 346)])
    label(d, 1223, 320, "queries")
    d.save("readme-architecture")


def deployment():
    d = Diagram(1440, 600, "Deployment model",
                "1. An admin root provisions service accounts, tokens, and Duckling settings and hands the "
                "writer token to a separate writer root. 2. The writer root creates writer-owned databases, "
                "restricted shares, and grants. 3. A pipeline loads data through the writer's Duckling. "
                "4. Each reader attaches the share once and queries on its own read pool.")
    brand(d, "Deployment model", "Provision identities, then provision as the owner, then load and read")
    legend(d, 1408-legend_width(d), 36)

    code(d, 40, 118, "admin.tf")
    caption(d, 100, 216, "Admin root", "organization admin token")
    code(d, 40, 306, "writer.tf")
    caption(d, 100, 404, "Writer root", "writer token")
    d.dashed([(100, 266), (100, 286)])
    d.arrow([(100, 286), (100, 302)], PURPLE)
    label(d, 162, 276, "writer token", PAPER, PURPLE_TEXT)

    account(d, 330, 196, 610, 364, "Writer account", note="owns every database and share")
    account(d, 1052, 196, 240, 364, "Reader account", True)

    # 1. The admin root provisions both accounts.
    d.dashed([(164, 150), (1172, 150)])
    for x in [640, 1172]:
        d.dashed([(x, 150), (x, 180)])
        d.arrow([(x, 180), (x, 196)], PURPLE)
    step(d, 220, 150, 1)
    label(d, 420, 124, "service accounts, tokens, Duckling settings", PAPER, PURPLE_TEXT)

    # 2. The writer root provisions data objects inside the writer account.
    d.dashed([(164, 346), (854, 346)])
    for x in [650, 854]:
        d.dashed([(x, 346), (x, 362)])
        d.arrow([(x, 362), (x, 376)], PURPLE)
    step(d, 220, 346, 2)
    label(d, 470, 320, "databases, schemas, shares, grants", SAND, PURPLE_TEXT)

    # 3. The pipeline loads data through the writer's Duckling.
    pipeline(d, 40, 478)
    caption(d, 106, 548, "Pipeline")
    d.chip(370, 396, 98, 76, TEAL)
    caption(d, 419, 492, "R/W Duckling")
    d.database(590, 384, 120, 96)
    caption(d, 650, 492, "Database")
    share(d, 800, 405)
    caption(d, 854, 492, "Restricted share")
    d.arrow([(172, 508), (290, 508), (290, 434), (362, 434)])
    step(d, 290, 471, 3)
    label(d, 231, 482, "loads")
    flow(d, 476, 582, 434, "writes", SAND)
    flow(d, 716, 792, 434, "publishes", SAND)

    # 4. The reader attaches the share once and queries its read pool.
    pool(d, 1112, 378)
    caption(d, 1172, 492, "Read pool", "read-scaling token")
    d.arrow([(916, 434), (1104, 434)])
    step(d, 1022, 434, 4)
    label(d, 984, 404, "grant + attach")
    app(d, 1318, 393)
    caption(d, 1372, 492, "BI & apps")
    d.arrow([(1310, 434), (1240, 434)])
    label(d, 1275, 404, "queries")
    d.save("deployment-model")


def warehouse():
    d = Diagram(1320, 580, "Layered warehouse",
                "One writer account owns raw, transform, and marts databases. A pipeline loads raw "
                "orders. The Terraform-managed orders_latest view deduplicates them. The pipeline "
                "then refreshes the daily_revenue table. BI connects with the writer's read-scaling "
                "token, which can read every layer through the writer's read pool.")
    brand(d, "Layered warehouse", "Terraform defines the layers. The pipeline loads and refreshes them.")
    legend(d, 1288-legend_width(d, access=True, terraform=False), 36, access=True, terraform=False)

    pipeline(d, 36, 234)
    caption(d, 102, 306, "Pipeline", "loads and refreshes")

    account(d, 216, 124, 924, 424, "Writer account", note="one identity for every layer")
    for x, name, obj in [(268, "Raw", "orders table"),
                          (548, "Transform", "orders_latest view"),
                          (828, "Marts", "daily_revenue table")]:
        d.database(x, 214, 124, 96)
        d.centered_text(x+62, 324, name, 20, True)
        d.centered_text(x+62, 352, obj, 14, color=MUTED)
    flow(d, 174, 260, 264, "loads")
    flow(d, 400, 540, 264, "view reads", SAND)
    flow(d, 680, 820, 264, "refreshes", SAND)

    # A bracket under all three layers shows the read-scaling token's reach.
    d.line([(268, 384), (268, 394), (952, 394), (952, 384)], ACCESS)
    d.line([(610, 394), (610, 452), (972, 452)], ACCESS)
    d.arrow([(960, 452), (982, 452)], ACCESS)
    label(d, 790, 404, "read-scaling token reads every layer", SAND, ACCESS)
    pool(d, 990, 410)
    caption(d, 1050, 506, "Read pool")

    app(d, 1206, 411)
    caption(d, 1260, 506, "BI")
    d.arrow([(1118, 452), (1198, 452)])
    label(d, 1162, 422, "queries")
    d.save("layered-warehouse")


def customers():
    d = Diagram(1320, 604, "Customer-facing analytics",
                "A pipeline writes each tenant's data through one central writer that owns the Acme "
                "and Globex databases and their restricted shares. Terraform grants each share only "
                "to its tenant's reader account, which attaches it once and queries on its own read "
                "pool. The backend authenticates the user and routes queries to the matching reader.")
    brand(d, "Customer-facing analytics", "One writer owns tenant data. Each tenant reads on its own account.")
    legend(d, 1288-legend_width(d, terraform=False), 36, terraform=False)

    pipeline(d, 36, 346)
    caption(d, 102, 418, "Pipeline", "writes each tenant")

    account(d, 206, 126, 470, 446, "Central writer account")
    for y, tenant in [(200, "Acme"), (408, "Globex")]:
        d.database(250, y, 124, 96)
        d.centered_text(312, y+112, tenant+" database", 17, True)
        share(d, 516, y+20)
        d.centered_text(570, y+112, "Restricted share", 17, True)
        flow(d, 382, 508, y+49, "publishes", SAND)
        account(d, 806, y-44, 260, 190, tenant+" reader", True)
        pool(d, 890, y+14)
        d.arrow([(632, y+49), (882, y+49)])
        label(d, 736, y+23, "grant + attach")
    d.line([(230, 376), (652, 376)], "#D8CFC5", 1)
    d.arrow([(170, 376), (200, 376), (200, 249), (242, 249)])
    d.arrow([(200, 376), (200, 457), (242, 457)])

    app(d, 1170, 316)
    caption(d, 1224, 420, "Backend", "auth + tenant routing")
    # Query arrows point from the backend to the selected tenant's pool.
    d.arrow([(1162, 341), (1136, 341), (1136, 249), (1018, 249)])
    d.arrow([(1162, 378), (1136, 378), (1136, 457), (1018, 457)])
    for y in [223, 431]:
        label(d, 1101, y, "queries")
    d.save("customer-facing-analytics")


def promotion():
    d = Diagram(1320, 540, "Blueprints code promotion",
                "Terraform first provisions the staging and production accounts. With staging enabled, "
                "each pull request deploys a preview and a merge to main deploys staging, both under the "
                "staging account. An approved release deploys its exact tag under the separate "
                "production account. Arrows promote code, not database contents.")
    brand(d, "Blueprints promotion", "Terraform provisions identities. Blueprints deploys code.")
    legend(d, 1288-legend_width(d, data_label="Code promotion"), 36, data_label="Code promotion")

    for x, tab, title in [(250, "pull request", "PR preview"),
                           (580, "main", "Staging"),
                           (1060, "release tag", "Production")]:
        code(d, x, 120, tab, tint="#E6F4F1")
        d.centered_text(x+56, 228, title, 19, True)
    flow(d, 378, 572, 164, "merge")
    flow(d, 708, 1052, 164, "release + approval")

    account(d, 200, 330, 600, 160, "Staging account")
    account(d, 840, 330, 452, 160, "Production account", True)
    for x in [306, 636, 1116]:
        d.arrow([(x, 262), (x, 322)])
        d.text(x+10, 280, "deploys", 13, color=MUTED)

    d.rect(226, 400, 240, 58, TEAL, stroke="#8CBEB3", radius=4)
    d.centered_text(346, 408, "Preview resources", 16, True)
    d.centered_text(346, 432, "removed when the PR closes", 13, color=MUTED)
    d.rect(526, 400, 240, 58, TEAL, stroke="#8CBEB3", radius=4)
    d.centered_text(646, 418, "Staging resources", 16, True)
    d.rect(916, 400, 300, 58, BLUE, stroke=READER_STROKE, radius=4)
    d.centered_text(1066, 418, "Production resources", 16, True)

    # Terraform sits beside the accounts it creates, outside the promotion row.
    code(d, 24, 330, "identities.tf")
    caption(d, 84, 438, "Terraform", "runs first")
    d.dashed([(145, 372), (186, 372)])
    d.arrow([(186, 372), (200, 372)], PURPLE)
    d.dashed([(84, 486), (84, 514), (1066, 514), (1066, 504)])
    d.arrow([(1066, 506), (1066, 490)], PURPLE)
    label(d, 560, 504, "accounts, tokens, compute", PAPER, PURPLE_TEXT)
    d.save("blueprints-promotion")


if __name__ == "__main__":
    deployment()
    warehouse()
    customers()
    promotion()
    readme()
