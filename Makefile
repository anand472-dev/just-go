GOCYCLO_VERSION := v0.6.0

.PHONY: build test vet oracle corpus conformance cover cyclo bench check tools

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

conformance: build
	JUST_GO_BIN=bin/just-go go test ./conformance/...

cover:
	scripts/check-coverage.sh

cyclo:
	gocyclo -over 9 .

bench:
	mkdir -p docs/benchmarks
	go test -bench=. -benchmem ./... | tee docs/benchmarks/go-bench.txt

check: vet test conformance

# Developer tool, not a module dependency.
tools:
	go install github.com/fzipp/gocyclo/cmd/gocyclo@$(GOCYCLO_VERSION)
