#!/bin/sh
# pre-commit: run goimports on staged Go files (auto-fix + re-stage)
if command -v goimports >/dev/null 2>&1; then
  goimports -w "$@"
fi
exit 0
