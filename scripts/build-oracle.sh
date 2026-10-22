#!/bin/sh
# Builds the pinned upstream `just` binary used as the conformance oracle.
# Output: oracle/bin/just. Stops with a non-zero status on any failure.
set -eu

PIN=d6fee036379c531369dc37f67f8d4640f6bf1e2d
REPO=https://github.com/casey/just.git

# Run from the repository root regardless of the caller's directory.
cd "$(dirname "$0")/.."

if [ ! -d oracle/src ]; then
	git clone "$REPO" oracle/src
fi
git -C oracle/src fetch --depth 1 origin "$PIN"
git -C oracle/src checkout --detach "$PIN"

HEAD_SHA=$(git -C oracle/src rev-parse HEAD)
if [ "$HEAD_SHA" != "$PIN" ]; then
	echo "oracle commit mismatch" >&2
	exit 1
fi

(cd oracle/src && cargo build --release --locked --bin just)

mkdir -p oracle/bin
cp oracle/src/target/release/just oracle/bin/just
