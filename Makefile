GOCYCLO_VERSION := v0.6.0

.PHONY: build test vet oracle corpus conformance-report baseline ratchet cover cyclo bench check tools triage-test cross

build:
	go build -o bin/just-go ./cmd/just-go

test:
	go test ./...

vet:
	go vet ./...

oracle:
	scripts/build-oracle.sh

corpus:
	scripts/record-corpus.sh

# The full corpus run. It prints pass, fail, and skip counts and may fail: most
# of the corpus is outside the milestone's scope (see conformance/corpus/README.md).
conformance-report: build
	@out=$$(mktemp); \
	JUST_GO_BIN=bin/just-go go test -v -count=1 -run '^TestConformance$$' ./conformance >"$$out" 2>&1; \
	status=$$?; \
	awk '/^ *--- (PASS|FAIL|SKIP): TestConformance\//{ n[$$2]++ } END { printf "TestConformance subtests: %d pass, %d fail, %d skip (of %d)\n", n["PASS:"], n["FAIL:"], n["SKIP:"], n["PASS:"]+n["FAIL:"]+n["SKIP:"] }' "$$out"; \
	rm -f "$$out"; \
	exit $$status

# The Plan 1 gate: every case in conformance/baseline.txt still passes.
baseline: build
	JUST_GO_BIN=bin/just-go go test -count=1 -run '^TestBaseline$$' ./conformance

# Adds the conformance cases bin/just-go passes now to conformance/baseline.txt.
ratchet: build
	JUST_GO_BIN=bin/just-go go run ./conformance/cmd/ratchet

cover:
	scripts/check-coverage.sh

cyclo:
	gocyclo -over 9 .

bench:
	mkdir -p docs/benchmarks
	go test -bench=. -benchmem ./... | tee docs/benchmarks/go-bench.txt

check: vet test baseline

# The unit tests of scripts/triage.py. Not part of check.
triage-test:
	python3 -m unittest conformance.triage_test_py

# Developer tool, not a module dependency.
tools:
	go install github.com/fzipp/gocyclo/cmd/gocyclo@$(GOCYCLO_VERSION)

# Compiles and vets for the platforms this machine cannot run: Windows, which the
# spec requires, and FreeBSD and OpenBSD, which are best-effort. It runs no tests;
# docs/platforms.md says which suites have run where.
cross:
	GOOS=windows GOARCH=amd64 go vet ./...
	GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/sys
	GOOS=freebsd GOARCH=amd64 go vet ./...
	GOOS=openbsd GOARCH=amd64 go vet ./...
