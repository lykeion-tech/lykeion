#!/bin/sh
set -eu
# Requires the pinned Emscripten 4.0.23 SDK and the extracted libhdf5-wasm
# HDF5-2.0.0-Emscripten.tar.gz archive. Verify the archive SHA-256 before extraction:
# d4dc6719ef164679728d9a50d6a4d97c826f563e22a40c4dec63e485b29781be
: "${EMCC:?path to Emscripten 4.0.23 emcc}"
: "${HDF5_WASM_ROOT:?path to verified extracted archive}"
cd "$(dirname "$0")"
SOURCE_DATE_EPOCH=0 "$EMCC" reader.c -I"$HDF5_WASM_ROOT/include" \
 "$HDF5_WASM_ROOT/lib/libhdf5.a" "$HDF5_WASM_ROOT/lib/libzlib-static.a" \
 "$HDF5_WASM_ROOT/lib/libaec.a" "$HDF5_WASM_ROOT/lib/libszaec.a" \
 -O2 -s MODULARIZE=1 -s EXPORT_ES6=1 -s ENVIRONMENT=web,worker -s FORCE_FILESYSTEM=1 \
 -s ALLOW_MEMORY_GROWTH=1 -s INITIAL_MEMORY=33554432 -s MAXIMUM_MEMORY=134217728 \
 -s EXPORTED_RUNTIME_METHODS=FS,UTF8ToString \
 -s EXPORTED_FUNCTIONS=_inspect,_item_count,_is_partial,_item_name,_item_type,_item_rank,_item_dim,_item_length,_item_value \
 -o hdf-reader.js
