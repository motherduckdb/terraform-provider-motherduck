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


# Every diagram uses the same visual grammar. Dashed purple lines are Terraform
# provisioning. Solid dark arrows are data or code movement. Blue lines are read
# access. Every arrow carries a short verb so the picture reads without the text.


def brand(d, title, detail):
    d.motherduck_mark(32, 26)
    d.text(83, 28, "MotherDuck", 27, bold=True)
    d.text(285, 33, title, 22)
    d.text(34, 83, detail, 15, color=MUTED)


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
    d.chip(x+27, y, 96, 66, BLUE)
    d.chip(x, y+27, 96, 66, BLUE)


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
    legend(d, 1408-legend_width(d), 40)

    code(d, 40, 134, "admin.tf")
    caption(d, 100, 240, "Admin root", "organization admin token")
    code(d, 40, 316, "writer.tf")
    caption(d, 100, 422, "Writer root", "writer token")
    d.dashed([(100, 290), (100, 300)])
    d.arrow([(100, 300), (100, 314)], PURPLE)
    label(d, 176, 292, "hands off writer token", PAPER, PURPLE_TEXT)

    account(d, 290, 192, 700, 368, "Writer account", note="owns every database and share")
    account(d, 1030, 192, 262, 368, "Reader account", True)

    # 1. Admin root provisions both accounts.
    d.dashed([(164, 160), (1220, 160)])
    for x in [640, 1220]:
        d.dashed([(x, 160), (x, 178)])
        d.arrow([(x, 178), (x, 192)], PURPLE)
    step(d, 240, 160, 1)
    label(d, 410, 134, "service accounts, tokens, Duckling settings", PAPER, PURPLE_TEXT)

    # 2. Writer root provisions data objects inside the writer account.
    d.dashed([(164, 362), (250, 362), (250, 300), (700, 300)])
    d.dashed([(700, 300), (700, 318)])
    d.arrow([(700, 318), (700, 330)], PURPLE)
    d.dashed([(700, 300), (880, 300), (880, 334)])
    d.arrow([(880, 334), (880, 346)], PURPLE)
    step(d, 250, 330, 2)
    label(d, 470, 286, "databases, schemas, shares, grants", SAND, PURPLE_TEXT)

    # 3. Pipeline loads data.
    pipeline(d, 40, 474)
    caption(d, 106, 544, "Pipeline")
    d.chip(338, 358, 98, 76, TEAL)
    caption(d, 387, 470, "R/W Duckling")
    d.database(640, 346, 120, 100)
    caption(d, 700, 470, "Database")
    share(d, 850, 367)
    caption(d, 904, 470, "Restricted share")
    d.arrow([(172, 504), (268, 504), (268, 396), (330, 396)])
    step(d, 220, 504, 3)
    label(d, 222, 470, "loads")
    flow(d, 444, 632, 396, "writes", SAND)
    flow(d, 766, 842, 396, "publishes", SAND)

    # 4. Readers attach and query.
    pool(d, 1090, 348)
    caption(d, 1152, 470, "Read pool", "read-scaling token")
    d.arrow([(966, 396), (1078, 396)])
    step(d, 1010, 396, 4)
    label(d, 1010, 342, "grant, then attach once", PAPER)
    app(d, 1318, 355)
    caption(d, 1372, 470, "BI & apps")
    d.arrow([(1310, 396), (1222, 396)])
    label(d, 1258, 370, "queries", READER_FILL)
    d.save("deployment-model")


def warehouse():
    d = Diagram(1320, 580, "Layered warehouse",
                "One writer account owns raw, transform, and marts databases. A pipeline loads raw "
                "orders. The Terraform-managed orders_latest view deduplicates them. The pipeline "
                "then refreshes the daily_revenue table. BI connects with the writer's read-scaling "
                "token, which can read every layer through the writer's read pool.")
    brand(d, "Layered warehouse", "Terraform defines the layers. The pipeline loads and refreshes them.")
    legend(d, 1288-legend_width(d, access=True, terraform=False), 40, access=True, terraform=False)

    pipeline(d, 36, 240)
    caption(d, 102, 312, "Pipeline", "loads and refreshes")

    account(d, 216, 124, 900, 424, "Writer account", note="one identity across all layers")
    for x, name, obj in [(270, "Raw", "orders table"),
                          (560, "Transform", "orders_latest view"),
                          (850, "Marts", "daily_revenue table")]:
        d.database(x, 214, 132, 100)
        d.centered_text(x+66, 330, name, 21, True)
        d.centered_text(x+66, 360, obj, 15, color=MUTED)
    flow(d, 174, 262, 270, "loads")
    flow(d, 410, 552, 264, "view reads raw", SAND)
    flow(d, 700, 842, 264, "pipeline refresh", SAND)

    # A common read path makes access to all layers explicit.
    d.line([(336, 392), (336, 446), (916, 446)], ACCESS)
    for x in [626, 916]:
        d.line([(x, 392), (x, 446)], ACCESS)
    d.arrow([(916, 446), (948, 446)], ACCESS)
    label(d, 480, 452, "read-scaling token reads every layer", SAND, ACCESS)
    pool(d, 962, 404)
    d.centered_text(1024, 512, "Read pool", 18, True)

    app(d, 1174, 405)
    caption(d, 1228, 512, "BI")
    d.arrow([(1098, 446), (1166, 446)])
    label(d, 1132, 416, "queries")
    d.save("layered-warehouse")


def customers():
    d = Diagram(1320, 604, "Customer-facing analytics",
                "A pipeline writes each tenant's data through one central writer that owns the Acme "
                "and Globex databases and their restricted shares. Terraform grants each share only "
                "to its tenant's reader account, which attaches it once and queries on its own read "
                "pool. The backend authenticates the user and routes queries to the matching reader.")
    brand(d, "Customer-facing analytics", "One writer owns tenant data. Each tenant reads on its own account.")
    legend(d, 1288-legend_width(d, terraform=False), 40, terraform=False)

    pipeline(d, 36, 346)
    caption(d, 102, 418, "Pipeline", "writes each tenant")

    account(d, 206, 126, 540, 446, "Central writer account")
    for y, tenant in [(200, "Acme"), (408, "Globex")]:
        d.database(250, y, 128, 100)
        d.centered_text(314, y+118, tenant+" database", 18, True)
        share(d, 560, y+24)
        d.centered_text(614, y+118, "Restricted share", 18, True)
        flow(d, 386, 552, y+54, "publishes", SAND)
        account(d, 790, y-44, 300, 190, tenant+" reader", True)
        pool(d, 890, y+22)
        flow(d, 676, 878, y+54, "grant + attach")
    d.line([(230, 376), (722, 376)], "#D8CFC5", 1)
    d.arrow([(170, 376), (200, 376), (200, 254), (242, 254)])
    d.arrow([(200, 376), (200, 462), (242, 462)])

    app(d, 1170, 316)
    caption(d, 1224, 420, "Backend", "auth + tenant routing")
    # Query arrows point from the backend to the selected tenant's pool.
    d.arrow([(1162, 341), (1130, 341), (1130, 254), (1030, 254)])
    d.arrow([(1162, 378), (1130, 378), (1130, 462), (1030, 462)])
    for y in [228, 436]:
        label(d, 1066, y, "queries", READER_FILL)
    d.save("customer-facing-analytics")


def promotion():
    d = Diagram(1320, 520, "Blueprints code promotion",
                "Terraform first provisions the staging and production accounts. With staging enabled, "
                "each pull request deploys a preview and a merge to main deploys staging, both under the "
                "staging account. An approved release deploys its exact tag under the separate "
                "production account. Arrows promote code, not database contents.")
    brand(d, "Blueprints promotion", "Terraform provisions identities. Blueprints deploys code.")
    legend(d, 1288-legend_width(d, data_label="Code promotion"), 40, data_label="Code promotion")

    for x, tab, title in [(220, "pull request", "PR preview"),
                           (560, "main", "Staging"),
                           (1040, "release tag", "Production")]:
        code(d, x, 132, tab, tint="#E6F4F1")
        d.centered_text(x+56, 242, title, 20, True)
    flow(d, 348, 552, 176, "merge")
    flow(d, 688, 1032, 176, "publish release + approval")

    account(d, 164, 330, 620, 160, "Staging account")
    account(d, 824, 330, 468, 160, "Production account", True)
    for x in [276, 616, 1096]:
        d.arrow([(x, 272), (x, 322)])
        d.text(x+10, 288, "deploys", 13, color=MUTED)

    d.rect(196, 400, 232, 58, TEAL, stroke="#8CBEB3", radius=4)
    d.centered_text(312, 408, "Preview resources", 17, True)
    d.centered_text(312, 432, "removed when the PR closes", 13, color=MUTED)
    d.rect(516, 400, 232, 58, TEAL, stroke="#8CBEB3", radius=4)
    d.centered_text(632, 418, "Staging resources", 17, True)
    d.rect(906, 400, 300, 58, BLUE, stroke=READER_STROKE, radius=4)
    d.centered_text(1056, 418, "Production resources", 17, True)

    code(d, 24, 132, "identities.tf")
    caption(d, 84, 242, "Terraform", "runs first")
    d.dashed([(84, 290), (84, 506), (1000, 506), (1000, 496)])
    d.arrow([(1000, 500), (1000, 490)], PURPLE)
    d.dashed([(84, 410), (150, 410)])
    d.arrow([(150, 410), (164, 410)], PURPLE)
    label(d, 540, 496, "accounts, tokens, compute", PAPER, PURPLE_TEXT)
    d.save("blueprints-promotion")


if __name__ == "__main__":
    deployment()
    warehouse()
    customers()
    promotion()
    readme()
