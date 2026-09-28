"""Regenerates the WebP/TIFF fixtures (Go has no WebP encoder).

Requires Pillow. Run from this directory: python gen.py (files are embedded by testimg.Fixture)
"""
from PIL import Image, ImageDraw


def scene(w, h, hue):
    im = Image.new("RGB", (w, h))
    d = ImageDraw.Draw(im)
    for y in range(h):
        d.line([(0, y), (w, y)], fill=(hue, 255 * y // h, 255 - hue))
    d.ellipse([w // 5, h // 5, w // 2, h // 2], fill=(250, 40, 40))
    d.rectangle([0, 0, w // 8, h // 8], fill=(0, 0, 0))  # top-left marker
    return im


base = scene(64, 48, 90)
base.save("still_lossy.webp", quality=90)
base.save("still_lossless.webp", lossless=True)

rgba = base.convert("RGBA")
rgba.putalpha(Image.linear_gradient("L").resize((64, 48)))
rgba.save("alpha_lossy.webp", quality=90)
rgba.save("alpha_lossless.webp", lossless=True)

frames = [scene(64, 48, 90), scene(64, 48, 200)]
frames[0].save("animated.webp", save_all=True, append_images=frames[1:], duration=100, loop=0)
frames[0].convert("RGBA").save(
    "animated_alpha.webp", save_all=True, append_images=[f.convert("RGBA") for f in frames[1:]],
    duration=100, loop=0, lossless=False, quality=90,
)

# Stored rotated 90 degrees counter-clockwise with orientation 6, so it
# displays like `base` (64x48, black marker top-left).
exif = Image.Exif()
exif[0x0112] = 6
rotated = base.transpose(Image.Transpose.ROTATE_90)
rotated.save("orient6.webp", quality=95, exif=exif)
rotated.save("orient6.tif", exif=exif)
base.save("plain.tif")
base.save("plain.bmp")
