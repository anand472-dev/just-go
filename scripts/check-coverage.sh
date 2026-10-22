#!/bin/sh
# Coverage gate (spec, Engineering standards 3): fails unless total statement
# coverage is above 95.0% and every function is at 100.0%.
#
# Coverage is measured with -coverpkg=./... so a package whose tests live in
# another package (internal/sys/sysfake is exercised from internal/sys) is
# credited for them.
#
# Usage: scripts/check-coverage.sh   (run from anywhere; it works from the
# module root)
set -eu

cd "$(dirname "$0")/.."

threshold=95.0
profile=$(mktemp "${TMPDIR:-/tmp}/just-go-cover.XXXXXX")
report=$(mktemp "${TMPDIR:-/tmp}/just-go-cover-func.XXXXXX")
trap 'rm -f "$profile" "$report"' EXIT

go test -coverpkg=./... -coverprofile="$profile" ./... >/dev/null
go tool cover -func="$profile" >"$report"

total=$(awk '$1 == "total:" { sub(/%$/, "", $NF); print $NF }' "$report")
under=$(awk '$1 != "total:" { p = $NF; sub(/%$/, "", p); if (p + 0 < 100) print }' "$report")

echo "total statement coverage: ${total}% (gate: above ${threshold}%)"

status=0
if ! awk -v t="$total" -v min="$threshold" 'BEGIN { exit !(t + 0 > min + 0) }'; then
	echo "FAIL: total statement coverage ${total}% is not above ${threshold}%" >&2
	status=1
fi
if [ -n "$under" ]; then
	echo "FAIL: functions below 100.0% coverage:" >&2
	echo "$under" >&2
	status=1
else
	echo "function coverage: every function at 100.0%"
fi
exit "$status"
