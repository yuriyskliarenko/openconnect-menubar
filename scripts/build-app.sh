#!/bin/bash
# Builds "dist/OpenConnect Menubar.app". With UNIVERSAL=1 it builds for Intel
# and Apple Silicon and also creates the release zip. VERSION overrides the
# version taken from git.
set -euo pipefail
cd "$(dirname "$0")/.."

APP="dist/OpenConnect Menubar.app"
VERSION=${VERSION:-$(git describe --tags --always 2>/dev/null || echo 0.1.0)}
VERSION=${VERSION#v}
export MACOSX_DEPLOYMENT_TARGET=13.0
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" dist/icon
BIN="$APP/Contents/MacOS/openconnect-menubar"
if [ "${UNIVERSAL:-}" = 1 ]; then
  for arch in amd64 arm64; do
    CGO_ENABLED=1 GOARCH=$arch go build -trimpath -o "dist/bin-$arch" .
  done
  lipo -create -output "$BIN" dist/bin-amd64 dist/bin-arm64
  rm dist/bin-amd64 dist/bin-arm64
else
  go build -trimpath -o "$BIN" .
fi

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
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
</dict></plist>
PLIST
codesign --force --deep --sign - "$APP" # ad-hoc: required on Apple Silicon
echo "Built $APP"

if [ "${UNIVERSAL:-}" = 1 ]; then
  ZIP="dist/OpenConnect-Menubar-$VERSION.zip"
  rm -f "$ZIP"
  ditto -c -k --sequesterRsrc --keepParent "$APP" "$ZIP"
  echo "Built $ZIP"
fi
