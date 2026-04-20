#!/bin/sh
# pre-commit: golangci-lint on new lines only
if ! command -v golangci-lint >/dev/null 2>&1; then
  echo "golangci-lint not installed — skipping (see https://golangci-lint.run)"
  exit 0
fi
golangci-lint run --new-from-rev=HEAD --fast ./... || exit 1
exit 0
