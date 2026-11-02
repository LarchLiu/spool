#!/usr/bin/env python3
"""Synthetic container/alpha/disposal fixtures; no user images or downloaded media."""
import io
import json
import struct
from pathlib import Path

from PIL import Image, ImageDraw, ImageCms

ROOT = Path(__file__).resolve().parents[2]
OUTPUT = ROOT / "android/app/src/test/resources/image-animation"
OUTPUT.mkdir(parents=True, exist_ok=True)
manifest = []


def save(name, data, description):
    (OUTPUT / name).write_bytes(data)
    manifest.append({"name": name, "description": description})


def tile(size, fill, box=None, color=None):
    image = Image.new("RGBA", size, fill)
    if box:
        ImageDraw.Draw(image).rectangle(box, fill=color)
    return image


frames = [tile((8, 6), (0, 0, 0, 0), box, color) for box, color in [
    ((0, 0, 3, 3), (255, 0, 0, 255)),
    ((2, 2, 5, 5), (0, 255, 0, 255)),
    ((4, 0, 7, 3), (0, 0, 255, 255)),
    ((0, 4, 7, 5), (255, 255, 0, 255)),
]]
for name, disposal, loop in [("restore.gif", [1, 3, 2, 1], 2), ("clear.gif", [2, 2, 2, 2], None)]:
    encoded = io.BytesIO()
    options = {} if loop is None else {"loop": loop}
    frames[0].save(encoded, format="GIF", save_all=True, append_images=frames[1:],
                   duration=[80, 120, 160, 200], disposal=disposal, optimize=True, **options)
    save(name, encoded.getvalue(), "GIF local/global palettes, cropped frames, transparency and disposal " + str(disposal))
encoded = io.BytesIO()
frames[0].save(encoded, format="GIF", save_all=True, append_images=frames[1:3],
               duration=[0, 10, 20], disposal=2, loop=0, optimize=True)
save("fast.gif", encoded.getvalue(), "GIF browser minimum-delay clamping and infinite loop")


def chunk(kind, data):
    return kind + struct.pack("<I", len(data)) + data + (b"\0" if len(data) % 2 else b"")


def u24(value):
    return value.to_bytes(3, "little")


def webp(name, lossless, loops, durations=None, pattern=False):
    width, height = 8, 6
    header = chunk(b"VP8X", b"\x12\0\0\0" + u24(width - 1) + u24(height - 1))
    header += chunk(b"ANIM", b"\x10\x20\x30\xff" + struct.pack("<H", loops))
    data = [
        (0, 0, tile((8, 6), (255, 0, 0, 255)), 80, 0),
        (2, 2, tile((4, 4), (0, 0, 0, 0), (1, 0, 3, 3), (0, 255, 0, 128)), 120, 1),
        (4, 0, tile((4, 4), (0, 0, 255, 255)), 160, 2),
        (0, 2, tile((8, 4), (0, 0, 0, 0), (0, 2, 7, 3), (255, 255, 0, 255)), 200, 2),
    ]
    if pattern:
        ImageDraw.Draw(data[0][2]).rectangle((4, 0, 7, 5), fill=(0, 0, 255, 255))
    for index, (x, y, image, duration, flags) in enumerate(data):
        if durations:
            duration = durations[index]
        encoded = io.BytesIO()
        image.save(encoded, format="WEBP", lossless=lossless, quality=100, method=6, exact=True)
        source = encoded.getvalue()
        at, pixels = 12, b""
        while at < len(source):
            size = struct.unpack_from("<I", source, at + 4)[0]
            end = at + 8 + size + size % 2
            if source[at:at + 4] in (b"ALPH", b"VP8 ", b"VP8L"):
                pixels += source[at:end]
            at = end
        frame = u24(x // 2) + u24(y // 2) + u24(image.width - 1) + u24(image.height - 1)
        frame += u24(duration) + bytes([flags]) + pixels
        header += chunk(b"ANMF", frame)
    body = b"WEBP" + header
    save(name, b"RIFF" + struct.pack("<I", len(body)) + body,
         "WebP VP8L" if lossless else "WebP VP8 + ALPH; cropped blend, no-blend and clear disposal")


webp("blend.webp", True, 2)
webp("alpha-lossy.webp", False, 0)
webp("fast.webp", True, 0, [0, 10, 20, 100])
webp("oriented-profile.webp", True, 2, pattern=True)
base = (OUTPUT / "oriented-profile.webp").read_bytes()
profile = ImageCms.ImageCmsProfile(ImageCms.createProfile("sRGB")).tobytes()
oriented = bytearray(base[12:30])
oriented[8] |= 0x28
exif = Image.Exif()
exif[274] = 6
body = b"WEBP" + oriented + chunk(b"ICCP", profile) + base[30:] + chunk(b"EXIF", exif.tobytes())
(OUTPUT / "oriented-profile.webp").write_bytes(b"RIFF" + struct.pack("<I", len(body)) + body)
manifest[-1]["description"] = "Animated WebP with ICC profile and EXIF clockwise orientation"
(OUTPUT / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
print(f"Generated {len(manifest)} bounded native/browser animation fixtures.")
