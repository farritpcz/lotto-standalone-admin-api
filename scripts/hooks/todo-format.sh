#!/bin/sh
# commit-msg: warn on TODO without owner+date
if grep -nE 'TODO[^(]' "$1" >/dev/null 2>&1; then
  echo "!  New TODO without owner+date? Prefer: TODO(farri, 2026-04-20): ..."
fi
exit 0
