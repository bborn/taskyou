#!/usr/bin/env bash
# Upload QA screenshots/gifs to the public R2 "evidence" bucket and print a
# ready-to-paste markdown image block for a PR comment.
#
# Usage:
#   ty-qa-publish.sh <prefix> <file> [file ...]
#     prefix   namespaces the objects, e.g. a PR number ("555")
#     file...  PNGs / GIFs / MP4s to upload
#
# Example:
#   ty-qa-publish.sh 555 /tmp/ty-qa/shots/*.png /tmp/ty-qa/shots/walkthrough.gif
#   # -> uploads, prints  ![welcome](https://pub-...r2.dev/taskyou-qa/<date>/555-welcome.png)
#
# Config (override via env). NOTE: no credentials live here — the rclone remote
# holds them. `r2-personal` is the remote with WRITE access to the bucket
# (the read-only `r2` remote returns 403 on PutObject).
TY_QA_R2_REMOTE="${TY_QA_R2_REMOTE:-r2-personal}"
TY_QA_R2_BUCKET="${TY_QA_R2_BUCKET:-qa-evidence}"
TY_QA_R2_KEYPREFIX="${TY_QA_R2_KEYPREFIX:-taskyou-qa}"
TY_QA_R2_PUBLIC="${TY_QA_R2_PUBLIC:-https://pub-e209f789a78e432384c9a13a5d956e7c.r2.dev}"

set -euo pipefail
command -v rclone >/dev/null || { echo "ty-qa: rclone not installed/configured" >&2; exit 1; }
[ "$#" -ge 2 ] || { echo "usage: ty-qa-publish.sh <prefix> <file> [file ...]" >&2; exit 1; }

PREFIX="$1"; shift
DATE="$(date +%F)"
DEST="$TY_QA_R2_REMOTE:$TY_QA_R2_BUCKET/$TY_QA_R2_KEYPREFIX/$DATE"
PUB="$TY_QA_R2_PUBLIC/$TY_QA_R2_KEYPREFIX/$DATE"

echo "==> uploading to $DEST" >&2
echo ""
echo "<!-- QA evidence (paste into the PR comment) -->"
for f in "$@"; do
  base="$(basename "$f")"
  key="$PREFIX-$base"
  ct="image/png"
  case "$base" in
    *.gif) ct="image/gif" ;;
    *.mp4) ct="video/mp4" ;;
    *.jpg|*.jpeg) ct="image/jpeg" ;;
  esac
  # --no-check-dest + --s3-no-head: the token can PutObject but not Head/List,
  # so skip rclone's existence/verify HEAD calls (they 403 otherwise).
  rclone copyto "$f" "$DEST/$key" --no-check-dest --s3-no-head \
    --header-upload "Content-Type: $ct" >/dev/null
  echo "![${base%.*}]($PUB/$key)"
done
