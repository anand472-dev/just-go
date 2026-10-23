package runner

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"gitea.com/wimwian/just-go/internal/diag"
	"gitea.com/wimwian/just-go/internal/sys"
	"gitea.com/wimwian/just-go/internal/sys/sysfake"
)

// hostPlan parses src and returns a plan that runs it against a fake
// filesystem that holds files and a fake process, in the directory /proj,
// with the justfile at /proj/justfile and the command run from /proj/here.
func hostPlan(t testing.TB, src string, files sysfake.FakeFS) (Plan, *sysfake.FakeProcess) {
	t.Helper()
	fake := &sysfake.FakeProcess{}
	plan, _, _ := newPlan(t, src, fake)
	plan.Justfile.File = "/proj/justfile"
	plan.WorkDir, plan.InvocationDir, plan.FS = "/proj", "/proj/here", files
	return plan, fake
}

func TestRunPathBuiltinsReadTheFilesystemOfThePlan(t *testing.T) {
	plan, fake := hostPlan(t, `
text := read('f')
foo:
  echo {{ path_exists('f') }} {{ path_exists('g') }} {{ text }} {{ absolute_path('a/../b') }} {{ canonicalize('d') }}
`, sysfake.FakeFS{"/proj/f": "hi", "/proj/d/x": ""})
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo true false hi /proj/b /proj/d"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

func TestRunDirectoryBuiltinsNameThePlacesOfThePlan(t *testing.T) {
	plan, fake := hostPlan(t, `
foo:
  echo {{ justfile() }} {{ justfile_directory() }} {{ invocation_directory() }} {{ invocation_directory_native() }}
  echo {{ source_file() }} {{ source_directory() }} {{ module_file() }} {{ module_directory() }} [{{ module_path() }}]
`, sysfake.FakeFS{})
	mustRun(t, plan)
	want := []string{
		"echo /proj/justfile /proj /proj/here /proj/here",
		"echo /proj/justfile /proj /proj/justfile /proj []",
	}
	if got := commands(fake); !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// Without an invocation directory the plan's is the working directory.
func TestRunInvocationDirectoryDefaultsToTheWorkingDirectory(t *testing.T) {
	plan, fake := hostPlan(t, "foo:\n  echo {{ invocation_directory() }}\n", sysfake.FakeFS{})
	plan.InvocationDir = ""
	mustRun(t, plan)
	if got, want := commands(fake), []string{"echo /proj"}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// Relative paths start from the directory set working-directory names, as
// upstream's working_directory does.
func TestRunRelativePathsStartFromTheWorkingDirectorySetting(t *testing.T) {
	plan, fake := hostPlan(t, "set working-directory := 'sub'\nfoo:\n  echo {{ absolute_path('x') }} {{ path_exists('f') }}\n",
		sysfake.FakeFS{"/proj/f": "", "/proj/sub/f": ""})
	mustRun(t, plan)
	want := []string{"echo " + filepath.Join("/proj", "sub", "x") + " true"}
	if got := commands(fake); !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// functions::read_file_not_found#1: the error is a runtime error at the call.
func TestRunReadOfAMissingFileIsARuntimeError(t *testing.T) {
	plan, fake := hostPlan(t, "foo:\n  echo {{ read('bar') }}\n", sysfake.FakeFS{})
	res, err := Run(context.Background(), plan)
	var de *diag.Error
	if !errors.As(err, &de) || de.Kind != diag.KindRuntime || de.Msg != "I/O error reading `bar`: No such file or directory (os error 2)" {
		t.Fatalf("err = %#v, want the missing file error", err)
	}
	if len(fake.Specs) != 0 || res.ExitCode != 0 {
		t.Errorf("ran %d commands, exit %d; want none", len(fake.Specs), res.ExitCode)
	}
}

// On Windows invocation_directory is what cygpath --unix makes of the native
// directory, run in the plan's directory; the native one is not translated.
func TestRunInvocationDirectoryOnWindowsIsTranslatedByCygpath(t *testing.T) {
	plan, fake := hostPlan(t, "foo:\n  echo {{ invocation_directory() }} {{ invocation_directory_native() }}\n", sysfake.FakeFS{})
	plan.OS, plan.Cygpath, plan.InvocationDir = "windows", "mycygpath", `C:\proj\here`
	fake.Outputs = []string{"/c/proj/here\n"}
	mustRun(t, plan)
	spec := fake.Specs[0]
	if len(spec.Argv) != 3 || spec.Argv[0] != "mycygpath" || spec.Argv[1] != "--unix" || spec.Argv[2] != `C:\proj\here` || spec.Dir != "/proj" {
		t.Errorf("cygpath ran as %+v", spec)
	}
	if got, want := commands(fake)[1:], []string{`echo /c/proj/here C:\proj\here`}; !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

func TestRunInvocationDirectoryOnWindowsKeepsThePathWhenCygpathFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		err  error
		out  string
	}{
		{"a status", 1, nil, "/ignored"}, {"a signal", -1, &sys.SignalError{Signal: 9}, ""},
		{"no program", -1, errors.New("not found"), ""}, {"output that is not text", 0, nil, "\xff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, fake := hostPlan(t, "foo:\n  echo {{ invocation_directory() }}\n", sysfake.FakeFS{})
			plan.OS, plan.InvocationDir = "windows", `C:\here`
			fake.Codes, fake.Errs, fake.Outputs = []int{tc.code}, []error{tc.err}, []string{tc.out}
			mustRun(t, plan)
			if fake.Specs[0].Argv[0] != "cygpath" {
				t.Errorf("ran %v first, want the default cygpath", fake.Specs[0].Argv)
			}
			if got, want := commands(fake)[1:], []string{`echo C:\here`}; !slices.Equal(got, want) {
				t.Errorf("ran %q, want %q", got, want)
			}
		})
	}
}

func BenchmarkRunPathBuiltins(b *testing.B) {
	plan, _ := hostPlan(b, "foo:\n  echo {{ path_exists('f') }} {{ absolute_path('a/../b') }} {{ justfile_directory() }}\n",
		sysfake.FakeFS{"/proj/f": ""})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Run(context.Background(), plan); err != nil {
			b.Fatal(err)
		}
	}
}
