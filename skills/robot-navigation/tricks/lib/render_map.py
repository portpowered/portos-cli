"""Reference map renderer: rooms, holes, a metre grid, the robot and its heading, its trail and
your places, drawn to a PNG with nothing outside the Python standard library. Adapt it freely.

    python3 tricks/lib/render_map.py scene.json map.png [--around x,y --radius 3] [--scale 60]

scene.json, all in metres in one map frame (the frame the pose's relativeToGroupRouteId names):

    {"rooms": [{"name": "Kitchen", "shape": <GeoJSON Polygon or MultiPolygon>}],
     "trail": [[x, y], ...],
     "robot": {"x": 1.0, "y": 2.0, "heading": 90},
     "places": [{"name": "Sofa", "x": 3.0, "y": 1.5}]}

Room shapes are the spatial/geometry attribute of the room groups; the robot and the trail come
from move replies. Labels are drawn in capitals with a small built-in font. +y points up.
"""

import argparse
import json
import math
import struct
import sys
import zlib

ROOM_FILLS = [(222, 235, 247), (229, 245, 224), (254, 237, 222), (239, 237, 245), (255, 247, 188), (224, 236, 244)]
OUTLINE = (90, 90, 90)
GRID = (236, 236, 236)
TRAIL = (33, 113, 181)
ROBOT = (203, 24, 29)
PLACE = (35, 139, 69)
TEXT = (40, 40, 40)
WHITE = (255, 255, 255)

# 3x5 glyphs, one row per string, for labels.
FONT = {
    "A": "010101111101101", "B": "110101110101110", "C": "011100100100011", "D": "110101101101110",
    "E": "111100110100111", "F": "111100110100100", "G": "011100101101011", "H": "101101111101101",
    "I": "111010010010111", "J": "001001001101010", "K": "101101110101101", "L": "100100100100111",
    "M": "101111111101101", "N": "110101101101101", "O": "010101101101010", "P": "110101110100100",
    "Q": "010101101110011", "R": "110101110101101", "S": "011100010001110", "T": "111010010010010",
    "U": "101101101101111", "V": "101101101101010", "W": "101101111111101", "X": "101101010101101",
    "Y": "101101010010010", "Z": "111001010100111", "0": "111101101101111", "1": "010110010010111",
    "2": "110001010100111", "3": "110001010001110", "4": "101101111001001", "5": "111100110001110",
    "6": "011100111101111", "7": "111001010010010", "8": "111101111101111", "9": "111101111001110",
    "-": "000000111000000", ".": "000000000000010", " ": "000000000000000",
}


class Canvas:
    def __init__(self, width, height):
        self.width, self.height = width, height
        self.pixels = bytearray(WHITE * (width * height))

    def set(self, x, y, colour):
        if 0 <= x < self.width and 0 <= y < self.height:
            i = 3 * (y * self.width + x)
            self.pixels[i:i + 3] = bytes(colour)

    def line(self, x0, y0, x1, y1, colour, width=1):
        steps = int(max(abs(x1 - x0), abs(y1 - y0), 1))
        for s in range(steps + 1):
            x = round(x0 + (x1 - x0) * s / steps)
            y = round(y0 + (y1 - y0) * s / steps)
            for dx in range(-(width // 2), width - width // 2):
                for dy in range(-(width // 2), width - width // 2):
                    self.set(x + dx, y + dy, colour)

    def disc(self, cx, cy, radius, colour):
        for y in range(int(cy - radius), int(cy + radius) + 1):
            for x in range(int(cx - radius), int(cx + radius) + 1):
                if (x - cx) ** 2 + (y - cy) ** 2 <= radius * radius:
                    self.set(x, y, colour)

    def fill_rings(self, rings, colour):
        """Fills a polygon with holes (even-odd), given rings of pixel points."""
        edges = [(ring[i], ring[(i + 1) % len(ring)]) for ring in rings for i in range(len(ring))]
        top = max(0, int(min(p[1] for ring in rings for p in ring)))
        bottom = min(self.height - 1, int(max(p[1] for ring in rings for p in ring)))
        for y in range(top, bottom + 1):
            scan = y + 0.5
            xs = sorted(a[0] + (scan - a[1]) * (b[0] - a[0]) / (b[1] - a[1])
                        for a, b in edges if (a[1] <= scan) != (b[1] <= scan))
            for left, right in zip(xs[0::2], xs[1::2]):
                for x in range(max(0, int(math.ceil(left - 0.5))), min(self.width, int(right + 0.5))):
                    self.set(x, y, colour)

    def text(self, x, y, label, colour, size=2):
        for index, char in enumerate(label.upper()):
            glyph = FONT.get(char, FONT[" "])
            for bit, on in enumerate(glyph):
                if on == "1":
                    gx, gy = bit % 3, bit // 3
                    for dx in range(size):
                        for dy in range(size):
                            self.set(x + index * 4 * size + gx * size + dx, y + gy * size + dy, colour)

    def png(self):
        rows = b"".join(b"\x00" + bytes(self.pixels[3 * y * self.width:3 * (y + 1) * self.width]) for y in range(self.height))
        def chunk(kind, data):
            return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
        header = struct.pack(">IIBBBBB", self.width, self.height, 8, 2, 0, 0, 0)
        return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) + chunk(b"IDAT", zlib.compress(rows, 9)) + chunk(b"IEND", b"")


def polygons(shape):
    """The polygons (lists of rings of [x, y]) of a GeoJSON Polygon or MultiPolygon."""
    if shape["type"] == "Polygon":
        return [shape["coordinates"]]
    if shape["type"] == "MultiPolygon":
        return shape["coordinates"]
    raise ValueError("unsupported shape type %s" % shape["type"])


def render(scene, scale=60.0, around=None, radius=3.0, margin=0.5):
    points = [tuple(p[:2]) for room in scene.get("rooms", []) for poly in polygons(room["shape"]) for ring in poly for p in ring]
    points += [tuple(p[:2]) for p in scene.get("trail", [])]
    points += [(p["x"], p["y"]) for p in scene.get("places", [])]
    if scene.get("robot"):
        points.append((scene["robot"]["x"], scene["robot"]["y"]))
    if around is not None:
        min_x, max_x, min_y, max_y = around[0] - radius, around[0] + radius, around[1] - radius, around[1] + radius
    elif points:
        min_x, max_x = min(p[0] for p in points) - margin, max(p[0] for p in points) + margin
        min_y, max_y = min(p[1] for p in points) - margin, max(p[1] for p in points) + margin
    else:
        raise ValueError("the scene is empty")
    width, height = int((max_x - min_x) * scale) + 1, int((max_y - min_y) * scale) + 1
    canvas = Canvas(width, height)

    def px(x, y):
        return (x - min_x) * scale, (max_y - y) * scale

    for gx in range(int(math.floor(min_x)), int(math.ceil(max_x)) + 1):
        canvas.line(*px(gx, min_y), *px(gx, max_y), GRID)
    for gy in range(int(math.floor(min_y)), int(math.ceil(max_y)) + 1):
        canvas.line(*px(min_x, gy), *px(max_x, gy), GRID)
    for index, room in enumerate(scene.get("rooms", [])):
        for poly in polygons(room["shape"]):
            rings = [[px(*p[:2]) for p in ring] for ring in poly]
            canvas.fill_rings(rings, ROOM_FILLS[index % len(ROOM_FILLS)])
            for ring in rings:
                for a, b in zip(ring, ring[1:] + ring[:1]):
                    canvas.line(*a, *b, OUTLINE)
    for room in scene.get("rooms", []):
        outer = polygons(room["shape"])[0][0]
        cx = sum(p[0] for p in outer) / len(outer)
        cy = sum(p[1] for p in outer) / len(outer)
        x, y = px(cx, cy)
        name = room.get("name", "")
        left = min(max(2, int(x) - 4 * len(name)), max(2, canvas.width - 8 * len(name) - 2))
        canvas.text(left, int(y) - 5, name, TEXT)
    trail = [px(*p[:2]) for p in scene.get("trail", [])]
    for a, b in zip(trail, trail[1:]):
        canvas.line(*a, *b, TRAIL, width=2)
    for place in scene.get("places", []):
        x, y = px(place["x"], place["y"])
        canvas.disc(x, y, 5, PLACE)
        canvas.text(int(x) + 8, int(y) - 5, place.get("name", ""), PLACE)
    robot = scene.get("robot")
    if robot:
        x, y = px(robot["x"], robot["y"])
        canvas.disc(x, y, max(4, 0.17 * scale), ROBOT)
        if robot.get("heading") is not None:
            angle = math.radians(robot["heading"])
            canvas.line(x, y, x + math.cos(angle) * 0.4 * scale, y - math.sin(angle) * 0.4 * scale, ROBOT, width=3)
    return canvas


def main(argv=None):
    parser = argparse.ArgumentParser(description="Draw a map scene to a PNG.")
    parser.add_argument("scene")
    parser.add_argument("output")
    parser.add_argument("--scale", type=float, default=60.0, help="pixels per metre")
    parser.add_argument("--around", help="x,y to crop around (default: everything)")
    parser.add_argument("--radius", type=float, default=3.0, help="metres around --around")
    args = parser.parse_args(argv)
    with open(args.scene) as source:
        scene = json.load(source)
    around = tuple(float(v) for v in args.around.split(",")) if args.around else None
    canvas = render(scene, args.scale, around, args.radius)
    with open(args.output, "wb") as target:
        target.write(canvas.png())
    print(json.dumps({"map": args.output, "width": canvas.width, "height": canvas.height}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
