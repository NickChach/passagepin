#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./build.sh [--install-deps]

Rebuild the bundled Go worker and Linux x86-64 desktop packages.
Uses .models/ and .runtime/onnxruntime-linux-x64-1.22.0/ by default;
set MODEL_DIR and RUNTIME_DIR to use other local assets.

Dependencies are installed automatically if desktop/node_modules is missing.
Use --install-deps to reinstall from package-lock.json after dependency changes.
EOF
}

install_deps=false
case "${1:-}" in
  '') ;;
  --install-deps) install_deps=true ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac
if (( $# > 1 )); then
  usage >&2
  exit 2
fi

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$repo_root"

if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  echo 'This build currently supports Linux x86-64 only.' >&2
  exit 1
fi
for tool in go node npm make cc tar; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "Missing build tool: $tool" >&2
    exit 1
  fi
done

model_dir="${MODEL_DIR:-.models}"
runtime_dir="${RUNTIME_DIR:-.runtime/onnxruntime-linux-x64-1.22.0}"
for asset in "$model_dir/model.onnx" "$model_dir/vocab.txt" \
  "$runtime_dir/VERSION_NUMBER" "$runtime_dir/lib/libonnxruntime.so.1.22.0"; do
  if [[ ! -s "$asset" ]]; then
    echo "Missing local asset: $asset (see README.md for setup)." >&2
    exit 1
  fi
done

(
  cd desktop
  if [[ "$install_deps" == true || ! -d node_modules ]]; then
    npm ci
  fi
  # Electron 45 installs its runtime explicitly; this reuses it when present.
  npm exec --no -- install-electron
)

# Pin staging to the worker rebuilt here, even if PDFSEARCH_WORKER was set for
# development. Both backend and frontend changes must reach the new packages.
CGO_ENABLED=1 PDFSEARCH_WORKER="$repo_root/dist/pdfsearch-linux-amd64" \
  make desktop-package MODEL_DIR="$model_dir" RUNTIME_DIR="$runtime_dir" \
    BUNDLE_OUT=dist/pdfsearch-linux-amd64

printf '\nBuild complete. Packages: %s/desktop/releases/\n' "$repo_root"
printf 'Launch the unpacked app: %q\n' "$repo_root/desktop/releases/linux-unpacked/passagepin"
