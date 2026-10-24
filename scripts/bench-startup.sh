#!/bin/sh
# Startup benchmark: times RUNS invocations of `--list` on
# conformance/bench/justfile for the oracle (oracle/bin/just) and for
# bin/just-go, and prints the mean wall time of one invocation for each.
# Both binaries read the same file. Output goes to /dev/null, so the numbers
# measure start, parse, and list formatting, not terminal speed.
#
# Each binary gets one untimed warm-up run first, so the page cache holds the
# binary and the justfile for both.
#
# The clock is perl's Time::HiRes (present on macOS and most Linux systems),
# because POSIX date has no sub-second resolution. One clock reading is taken
# before the loop and one after it, so the clock's own cost is spread over
# RUNS invocations.
#
# Usage: sh scripts/bench-startup.sh   (RUNS=50 sh scripts/bench-startup.sh
# for more runs)
set -eu

cd "$(dirname "$0")/.."

runs=${RUNS:-20}
justfile=conformance/bench/justfile

now() {
	perl -MTime::HiRes=time -e 'printf "%.6f\n", time'
}

bench() {
	bin=$1
	if [ ! -x "$bin" ]; then
		echo "bench-startup: $bin is missing; run 'just oracle' and 'just build'" >&2
		exit 1
	fi
	"$bin" --justfile "$justfile" --list >/dev/null
	start=$(now)
	i=0
	while [ "$i" -lt "$runs" ]; do
		"$bin" --justfile "$justfile" --list >/dev/null
		i=$((i + 1))
	done
	end=$(now)
	awk -v s="$start" -v e="$end" -v n="$runs" -v b="$bin" \
		'BEGIN { printf "%-16s mean %.2f ms over %d runs\n", b, (e - s) * 1000 / n, n }'
}

bench oracle/bin/just
bench bin/just-go
