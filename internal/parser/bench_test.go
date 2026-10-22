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
