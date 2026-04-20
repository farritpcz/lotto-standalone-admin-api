#!/bin/sh
# pre-commit: vet only the packages containing staged files
# (so pre-existing errors in other packages don't block commits)
dirs=$(printf '%s\n' "$@" | xargs -n1 dirname | sort -u)
fail=0
for d in $dirs; do
  # Skip dirs that have no Go files in scope (e.g. all files tagged //go:build ignore)
  out=$(go vet "./$d/..." 2>&1)
  rc=$?
  # "no packages to vet" — acceptable, means all files are build-ignored
  if [ $rc -ne 0 ] && ! echo "$out" | grep -q "no packages to vet\|matched no packages"; then
    echo "$out"
    fail=1
  fi
done
exit $fail
