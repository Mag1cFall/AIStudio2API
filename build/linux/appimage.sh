#!/usr/bin/env bash
# appimage 用 wails3 generate appimage 打包 Linux 桌面版，再放入构建机的 GIO 模块，并让随包的 GLib 与 GTK 只加载随包的 GIO 模块与输入法模块
# 用法：build/linux/appimage.sh <桌面版可执行文件> <输出 AppImage>
set -euo pipefail
binary=$(realpath "$1")
output=$(realpath "$2")
work=$(mktemp -d)
cp build/appicon.png "$work/aistudio2api.png"
wails3 generate appimage -binary "$binary" -icon "$work/aistudio2api.png" -desktopfile build/linux/aistudio2api.desktop -outputdir "$work" -builddir "$work/build"
appdir=$work/build/aistudio2api-x86_64.AppDir
mkdir -p "$appdir/usr/lib/gio"
cp -r "$(pkg-config --variable=giomoduledir gio-2.0)" "$appdir/usr/lib/gio/modules"
sed -i 's|^cd "\$usr/lib"$|export GIO_MODULE_DIR="$usr/lib/gio/modules" GTK_IM_MODULE_FILE="$usr/lib/gtk-3.0/3.0.0/immodules.cache"\n&|' "$appdir/AppRun"
(cd "$work" && OUTPUT=repacked.AppImage build/linuxdeploy-x86_64.AppImage --appimage-extract-and-run --appdir "$appdir" --output appimage)
mv "$work/repacked.AppImage" "$output"
rm -rf "$work"
