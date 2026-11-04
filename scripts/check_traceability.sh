#!/usr/bin/env bash
# check_traceability.sh — enforce requirement traceability (Issue #75).
#
# Rules:
#   1. Every `*_test.go` path referenced anywhere in docs/REQUIREMENTS.md must exist.
#   2. Every `[x] Complete` row must cite a proof that exists:
#        - a `*_test.go::TestName` that is defined in that file, OR
#        - a `gate:<path>` where <path> exists, OR
#        - an existing repository file path (e.g. Dockerfile, Makefile, a .css/.png).
#   3. Every cited test function must actually be defined in the cited file.
#
# Exits non-zero (failing CI) on any violation.
set -euo pipefail

REQ="${REQUIREMENTS_FILE:-docs/REQUIREMENTS.md}"
[ -f "$REQ" ] || { echo "::error::missing $REQ"; exit 1; }

fail=0

# --- Rule 1: referenced test files must exist ---------------------------------
while IFS= read -r f; do
  [ -z "$f" ] && continue
  if [ ! -f "$f" ]; then
    echo "::error::SRS references a test file that does not exist: $f"
    fail=1
  fi
done < <(grep -oE '[A-Za-z0-9_./-]+_test\.go' "$REQ" | sort -u)

# --- Extract Complete rows as "<ID>\t<proof-cell>" ----------------------------
rows="$(awk -F'|' '
  /^\| \*\*`[A-Z]+-[0-9]+`\*\*/ {
    id=$2; gsub(/[`* ]/, "", id);
    status=$4;
    proof=$8;
    if (status ~ /Complete/) { printf "%s\t%s\n", id, proof }
  }' "$REQ")"

if [ -z "$rows" ]; then
  echo "::error::no '[x] Complete' rows found — check the matrix format"
  exit 1
fi

# --- Rules 2 & 3 --------------------------------------------------------------
while IFS=$'\t' read -r id proof; do
  [ -z "$id" ] && continue
  ok=0

  # (a) Go test proofs: validate file + function.
  tokens="$(printf '%s' "$proof" | grep -oE '[A-Za-z0-9_./-]+_test\.go::Test[A-Za-z0-9_]+' | sort -u || true)"
  while IFS= read -r tok; do
    [ -z "$tok" ] && continue
    f="${tok%%::*}"; t="${tok##*::}"
    if [ ! -f "$f" ]; then
      echo "::error::$id cites missing test file: $f"; fail=1
    elif ! grep -qE "func ${t}(\[|\(|\b)" "$f"; then
      echo "::error::$id cites undefined test ${t} in ${f}"; fail=1
    else
      ok=1
    fi
  done <<< "$tokens"

  # (b) gate:<path> proofs must exist.
  while IFS= read -r g; do
    [ -z "$g" ] && continue
    p="${g#gate:}"
    if [ -e "$p" ]; then ok=1; else echo "::error::$id gate path missing: $p"; fail=1; fi
  done < <(printf '%s' "$proof" | grep -oE 'gate:[A-Za-z0-9_./-]+' || true)

  # (c) any existing repository path token (e.g. Dockerfile, x.css, screenshots).
  while IFS= read -r p; do
    [ -z "$p" ] && continue
    if [ -e "$p" ]; then ok=1; fi
  done < <(printf '%s' "$proof" | grep -oE '[A-Za-z0-9_.-]+/[A-Za-z0-9_./*-]+|[A-Za-z0-9_-]+\.(go|yml|yaml|md|sql|json|sh|css|png|svg)' || true)

  # (d) bare root artifact names.
  for p in Dockerfile Makefile docker-compose.yml; do
    if printf '%s' "$proof" | grep -q "$p" && [ -e "$p" ]; then ok=1; fi
  done

  if [ "$ok" -ne 1 ]; then
    echo "::error::$id is marked Complete but its proof references nothing that exists"
    fail=1
  fi
done <<< "$rows"

if [ "$fail" -ne 0 ]; then
  echo "Traceability check FAILED."
  exit 1
fi

echo "Traceability check passed: every Complete row cites an existing proof."