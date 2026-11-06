#!/bin/bash
# Builds "dist/OpenConnect Menubar.app".
set -euo pipefail
cd "$(dirname "$0")/.."

APP="dist/OpenConnect Menubar.app"
VERSION=$(git describe --tags --always 2>/dev/null || echo 0.1.0)
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" dist/icon
go build -o "$APP/Contents/MacOS/openconnect-menubar" .

ICONSET=dist/icon/AppIcon.iconset
mkdir -p "$ICONSET"
go run ./tools/icongen dist/icon/icon.png
for s in 16 32 128 256 512; do
  sips -z $s $s dist/icon/icon.png --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  d=$((s * 2))
  sips -z $d $d dist/icon/icon.png --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/AppIcon.icns"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>OpenConnect Menubar</string>
  <key>CFBundleIdentifier</key><string>io.github.yuriyskliarenko.openconnect-menubar</string>
  <key>CFBundleExecutable</key><string>openconnect-menubar</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>LSUIElement</key><true/>
</dict></plist>
PLIST
echo "Built $APP"
