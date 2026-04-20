#!/bin/sh
# pre-commit: block fmt.Println + hardcoded adminID := int64(1)
if grep -nE 'fmt\.Println' "$@" 2>/dev/null; then
  echo "X  Found fmt.Println — use slog/log (coding_standards.md 5)"
  exit 1
fi
if grep -nE 'adminID[[:space:]]*:=[[:space:]]*int64\(1\)' "$@" 2>/dev/null; then
  echo "X  Hardcoded adminID := int64(1) — use getAdminIDFromJWT helper"
  exit 1
fi
exit 0
