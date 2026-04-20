#!/bin/sh
# pre-commit: vet only the packages containing staged files
# (so pre-existing errors in other packages don't block commits)
dirs=$(printf '%s\n' "$@" | xargs -n1 dirname | sort -u)
fail=0
for d in $dirs; do
  go vet "./$d/..." || fail=1
done
exit $fail
