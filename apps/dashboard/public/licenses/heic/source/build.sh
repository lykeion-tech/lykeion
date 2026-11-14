#!/bin/sh
set -eu
# Requirements: Emscripten 4.0.23, CMake 3.31.6 and POSIX make on PATH.
base=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/source"
tar -xzf "$base/libheif-1.23.7.tar.gz" -C "$work/source"
tar -xzf "$base/libde265-1.1.3.tar.gz" -C "$work/source"
emcmake cmake -S "$work/source/libde265" -B "$work/build-de265" -DBUILD_SHARED_LIBS=OFF -DENABLE_SDL=OFF -DENABLE_DECODER=OFF -DENABLE_ENCODER=OFF -DCMAKE_BUILD_TYPE=Release
cmake --build "$work/build-de265" -j 4
emcmake cmake -S "$work/source/libheif" -B "$work/build-heif" -DENABLE_MULTITHREADING_SUPPORT=OFF -DWITH_GDK_PIXBUF=OFF -DWITH_EXAMPLES=OFF -DBUILD_SHARED_LIBS=OFF -DENABLE_PLUGIN_LOADING=OFF -DWITH_LIBDE265=ON -DWITH_LIBDE265_PLUGIN=OFF -DLIBDE265_INCLUDE_DIR="$work/source/libde265" -DLIBDE265_LIBRARY="$work/build-de265/libde265/libde265.a" -DCMAKE_CXX_FLAGS="-I$work/build-de265" -DCMAKE_BUILD_TYPE=Release
cmake --build "$work/build-heif" --target heif -j 4
emcc "$base/preview.c" -I"$work/source/libheif/libheif/api" -I"$work/build-heif" "$work/build-heif/libheif/libheif.a" "$work/build-de265/libde265/libde265.a" -lembind -sEXPORTED_FUNCTIONS='["_preview_decode","_preview_release","_preview_pixels","_preview_width","_preview_height","_malloc","_free"]' -sEXPORTED_RUNTIME_METHODS='["HEAPU8"]' -sMODULARIZE -sEXPORT_ES6 -sENVIRONMENT=worker -sALLOW_MEMORY_GROWTH -sMAXIMUM_MEMORY=134217728 -sDYNAMIC_EXECUTION=0 -O3 -o "$base/safe-heic.mjs"
echo "Rebuilt $base/safe-heic.mjs and $base/safe-heic.wasm"
