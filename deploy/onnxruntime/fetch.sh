#!/bin/sh
# Fetch only the native CPU libraries/notices; no .NET runtime is installed.
# Dockerfiles and real-model NER CI use the same pinned package and extraction.
set -eu

if [ "$#" -ne 2 ] || [ -z "$1" ]; then
    echo "usage: $0 OUTPUT_DIRECTORY amd64|arm64" >&2
    exit 2
fi
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=deploy/onnxruntime/manifest.env
. "$script_dir/manifest.env"
if [ "${ORT_VERSION:-$ORT_PACKAGE_VERSION}" != "$ORT_PACKAGE_VERSION" ]; then
    echo "ORT_VERSION must match deploy/onnxruntime/manifest.env ($ORT_PACKAGE_VERSION)" >&2
    exit 1
fi
case "$2" in
    amd64) rid=linux-x64 ;;
    arm64) rid=linux-arm64 ;;
    *) echo "unsupported ONNX Runtime architecture: $2" >&2; exit 2 ;;
esac

output=$1
package="$output/microsoft.ml.onnxruntime.$ORT_PACKAGE_VERSION.nupkg"
mkdir -p "$output/lib"
# Keep the downloaded package for build-cache reuse and failure inspection.
# Every reuse is verified too; an incomplete or changed package fails closed.
if [ ! -f "$package" ]; then
    curl --fail --show-error --silent --location --retry 3 --connect-timeout 20 --max-time 300 \
        "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/$ORT_PACKAGE_VERSION/microsoft.ml.onnxruntime.$ORT_PACKAGE_VERSION.nupkg" \
        --output "$package"
fi
printf '%s  %s\n' "$ORT_PACKAGE_SHA256" "$package" | sha256sum -c -
for library in libonnxruntime.so libonnxruntime_providers_shared.so; do
    unzip -p "$package" "runtimes/$rid/native/$library" > "$output/lib/$library"
    test -s "$output/lib/$library"
    chmod 755 "$output/lib/$library"
done
for notice in LICENSE ThirdPartyNotices.txt; do
    unzip -p "$package" "$notice" > "$output/$notice"
    test -s "$output/$notice"
    chmod 644 "$output/$notice"
done
# Match the SDK archive layout expected by dlopen consumers and image COPYs.
ln -sf libonnxruntime.so "$output/lib/libonnxruntime.so.1"
ln -sf libonnxruntime.so "$output/lib/libonnxruntime.so.$ORT_PACKAGE_VERSION"
printf '%s\n' "$ORT_PACKAGE_VERSION" > "$output/VERSION"
chmod -R a+rX "$output/lib"
printf 'ONNX Runtime %s (%s) ready in %s\n' "$ORT_PACKAGE_VERSION" "$rid" "$output"
