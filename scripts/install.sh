#!/usr/bin/env bash
# Build the autoshift-lsp Go binary and symlink the Lua plugin into
# Neovim's native pack path. Idempotent — re-run after pulling updates.
#
# Usage:
#   scripts/install.sh                 # build + symlink to default pack dir
#   scripts/install.sh --build-only
#   scripts/install.sh --uninstall
#   scripts/install.sh --pack-dir <path>
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
LSP_SRC="${REPO_ROOT}/lsp-server"
PLUGIN_SRC="${REPO_ROOT}"
DEFAULT_PACK_DIR="${HOME}/.local/share/nvim/site/pack/local/start"

PACK_DIR="${DEFAULT_PACK_DIR}"
BUILD_ONLY=0
UNINSTALL=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --pack-dir)
      PACK_DIR="$2"; shift 2;;
    --build-only)
      BUILD_ONLY=1; shift;;
    --uninstall)
      UNINSTALL=1; shift;;
    -h|--help)
      sed -n '2,12p' "${BASH_SOURCE[0]}"; exit 0;;
    *)
      echo "unknown arg: $1" >&2; exit 2;;
  esac
done

LINK_TARGET="${PACK_DIR}/autoshift"

if [[ "${UNINSTALL}" -eq 1 ]]; then
  if [[ -L "${LINK_TARGET}" ]]; then
    rm "${LINK_TARGET}"
    echo "removed symlink: ${LINK_TARGET}"
  else
    echo "no symlink at ${LINK_TARGET} — nothing to do"
  fi
  exit 0
fi

if ! command -v go >/dev/null 2>&1; then
  echo "error: go is not installed or not on PATH" >&2
  exit 1
fi

echo "building autoshift-lsp..."
(
  cd "${LSP_SRC}"
  go build -o autoshift-lsp .
)
BIN="${LSP_SRC}/autoshift-lsp"
echo "built: ${BIN}"

if [[ "${BUILD_ONLY}" -eq 1 ]]; then
  exit 0
fi

mkdir -p "${PACK_DIR}"
if [[ -L "${LINK_TARGET}" ]]; then
  current="$(readlink "${LINK_TARGET}")"
  if [[ "${current}" == "${PLUGIN_SRC}" ]]; then
    echo "symlink already correct: ${LINK_TARGET} -> ${PLUGIN_SRC}"
  else
    rm "${LINK_TARGET}"
    ln -s "${PLUGIN_SRC}" "${LINK_TARGET}"
    echo "updated symlink: ${LINK_TARGET} -> ${PLUGIN_SRC}"
  fi
elif [[ -e "${LINK_TARGET}" ]]; then
  echo "error: ${LINK_TARGET} exists and is not a symlink — refusing to overwrite" >&2
  exit 1
else
  ln -s "${PLUGIN_SRC}" "${LINK_TARGET}"
  echo "created symlink: ${LINK_TARGET} -> ${PLUGIN_SRC}"
fi

cat <<EOF

Done. To activate, add to your init.lua:

  require("autoshift").setup({
    cmd = { "${BIN}" },
  })

Recommended treesitter parsers:

  :TSInstall yaml gotmpl

Uninstall later with: scripts/install.sh --uninstall
EOF
