package parser_test

import (
	"fmt"
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/parser"
)

// generate returns a justfile of about n lines: assignments and recipes with
// parameters, dependencies and interpolated bodies.
func generate(n int) []byte {
	var b strings.Builder
	b.WriteString("set shell := [\"bash\", \"-cu\"]\n")
	for i := 0; strings.Count(b.String(), "\n") < n; i++ {
		fmt.Fprintf(&b, "v%d := \"value\" + \"%d\"\n", i, i)
		fmt.Fprintf(&b, "# recipe %d\n@r%d arg=\"x\": dep\n", i, i)
		fmt.Fprintf(&b, "  echo {{arg}} {{v%d}}\n  -false\n\n", i)
	}
	return []byte(b.String())
}

func BenchmarkParse(b *testing.B) {
	src := generate(1000)
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	for i := 0; i < b.N; i++ {
		if _, err := parser.Parse("justfile", src); err != nil {
			b.Fatal(err)
		}
	}
}

// generateDependencyArguments returns a justfile of n recipes in which each
// passes arguments to two dependencies, one of them after `&&`.
func generateDependencyArguments(n int) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "r%d x='a': (r%d x + 'b' 'c') && (r%d x)\n  echo {{x}}\n", i, i+1, i+2)
	}
	return []byte(b.String())
}

func BenchmarkParseDependencyArguments(b *testing.B) {
	src := generateDependencyArguments(500)
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		if _, err := parser.Parse("justfile", src); err != nil {
			b.Fatal(err)
		}
	}
}

// generateAttributes returns a justfile of n recipes that each carry several
// attributes, some with expression arguments.
func generateAttributes(n int) []byte {
	var b strings.Builder
	b.WriteString("prefix := 'p'\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "# recipe %d\n[group('g%d'), no-cd]\n[doc(prefix + 'doc')]\n[confirm('sure?')]\n@r%d x='a':\n  echo {{x}}\n", i, i%7, i)
	}
	return []byte(b.String())
}

func BenchmarkParseAttributes(b *testing.B) {
	src := generateAttributes(500)
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		if _, err := parser.Parse("justfile", src); err != nil {
			b.Fatal(err)
		}
	}
}

// generateGated returns a justfile of n pairs of recipes with the same name,
// one for Unix and one for Windows, each with an environment variable.
func generateGated(n int) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "[unix, env('K%d', 'unix')]\nr%d:\n  echo unix\n[windows, env('K%d', 'windows')]\nr%d:\n  echo windows\n", i, i, i, i)
	}
	return []byte(b.String())
}

func BenchmarkParseGated(b *testing.B) {
	src := generateGated(500)
	b.ReportAllocs()
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		if _, err := parser.ParseFor("justfile", src, "linux"); err != nil {
			b.Fatal(err)
		}
	}
}
