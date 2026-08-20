#!/usr/bin/env bash
# Generate elyfeed PWA icons into web/public/icons using ImageMagick.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/web/public/icons"
mkdir -p "$OUT"

# Prefer `magick` (ImageMagick 7), fall back to `convert` (ImageMagick 6).
if command -v magick >/dev/null 2>&1; then
  IM=magick
elif command -v convert >/dev/null 2>&1; then
  IM=convert
else
  echo "error: ImageMagick (magick or convert) not found" >&2
  exit 1
fi

# Pick a bold sans font; default to Noto Sans Bold, allow override via FONT.
FONT="${FONT:-}"
if [[ -z "$FONT" ]]; then
  for candidate in \
    /usr/share/fonts/google-noto/NotoSans-Bold.ttf \
    /usr/share/fonts/truetype/noto/NotoSans-Bold.ttf \
    /usr/share/fonts/liberation-sans-fonts/LiberationSans-Bold.ttf; do
    if [[ -f "$candidate" ]]; then FONT="$candidate"; break; fi
  done
fi
if [[ -z "$FONT" ]]; then
  echo "warning: no bold font found; ImageMagick will use a default font" >&2
  FONT=""
fi

font_args=()
if [[ -n "$FONT" ]]; then
  font_args=(-font "$FONT")
fi

ACCENT='#2563eb'

# Standard icon: full-bleed accent with rounded corners and a white 'e'.
"$IM" -size 512x512 xc:"$ACCENT" \
  \( -size 512x512 xc:none -fill white -draw 'roundrectangle 0,0 511,511 96,96' \) \
  -compose CopyOpacity -composite \
  -gravity center -pointsize 340 "${font_args[@]}" -fill white -annotate +0+12 'e' \
  "$OUT/icon-512.png"

"$IM" "$OUT/icon-512.png" -resize 192x192 "$OUT/icon-192.png"

# Maskable icon: full-bleed background (the OS applies the mask), glyph kept
# inside the central safe zone.
"$IM" -size 512x512 xc:"$ACCENT" \
  -gravity center -pointsize 280 "${font_args[@]}" -fill white -annotate +0+10 'e' \
  "$OUT/maskable-512.png"

echo "icons written to $OUT"
