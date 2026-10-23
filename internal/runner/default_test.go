package runner

import (
	"strings"
	"testing"

	"gitea.com/wimwian/just-go/internal/ast"
)

// A recipe that came from an import is never the default: a file whose only
// recipes are imported has no default recipe (imports::import_recipes_are_not_default).
func TestImportedRecipesAreNotTheDefault(t *testing.T) {
	jf := &ast.Justfile{Recipes: []ast.Recipe{{Name: "b", Unit: &ast.Justfile{File: "b"}}}}
	_, err := legacyCalls(jf, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "justfile contains no default recipe") {
		t.Errorf("legacyCalls = %v; want the no-default-recipe usage error", err)
	}
}
