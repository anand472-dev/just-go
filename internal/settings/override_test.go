package settings

import (
	"testing"

	"gitea.com/wimwian/just-go/internal/parser"
)

// A --set override of a variable that a setting reads is the value the setting
// sees, as upstream does: the override replaces the assignment's value.
func TestResolveWithOverrideReplacesTheVariable(t *testing.T) {
	jf, err := parser.Parse("justfile", []byte("dir := 'foo'\nset working-directory := dir + '-bob'\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := ResolveWith(jf, map[string]string{"dir": "bar"})
	if err != nil || got.WorkingDirectory == nil || *got.WorkingDirectory != "bar-bob" {
		t.Errorf("ResolveWith = %+v, %v; want working directory bar-bob", got.WorkingDirectory, err)
	}
	plain, err := Resolve(jf)
	if err != nil || plain.WorkingDirectory == nil || *plain.WorkingDirectory != "foo-bob" {
		t.Errorf("Resolve = %+v, %v; want foo-bob", plain.WorkingDirectory, err)
	}
}
