#!/usr/bin/env bash
# Extract a GoReleaser ty archive and exercise the documented first-use path.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
ARCHIVE="${1:-}"

if [[ -z "$ARCHIVE" ]]; then
    case "$(uname -s)" in
        Darwin) RELEASE_OS="darwin" ;;
        Linux) RELEASE_OS="linux" ;;
        *) echo "Unsupported release smoke-test OS: $(uname -s)" >&2; exit 1 ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) RELEASE_ARCH="amd64" ;;
        arm64|aarch64) RELEASE_ARCH="arm64" ;;
        *) echo "Unsupported release smoke-test architecture: $(uname -m)" >&2; exit 1 ;;
    esac

    shopt -s nullglob
    archives=("$REPO_ROOT"/dist/ty_*_"$RELEASE_OS"_"$RELEASE_ARCH".tar.gz)
    shopt -u nullglob
    if [[ ${#archives[@]} -ne 1 ]]; then
        echo "Expected one packaged ty archive for $RELEASE_OS/$RELEASE_ARCH in dist; found ${#archives[@]}" >&2
        exit 1
    fi
    ARCHIVE="${archives[0]}"
fi

if [[ ! -f "$ARCHIVE" ]]; then
    echo "Release archive does not exist: $ARCHIVE" >&2
    exit 1
fi

CHECK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ty-release-onboarding.XXXXXX")"
trap 'rm -rf "$CHECK_ROOT"' EXIT

LC_ALL=C tar -xzf "$ARCHIVE" -C "$CHECK_ROOT"
TY_BIN="$CHECK_ROOT/ty"
if [[ ! -x "$TY_BIN" ]]; then
    echo "Release archive does not contain an executable top-level ty binary: $ARCHIVE" >&2
    exit 1
fi

"$TY_BIN" --version
"$REPO_ROOT/scripts/qa/check-onboarding.sh" "$TY_BIN"
