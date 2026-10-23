package request

import "testing"

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}

// An environment variable that is set is answered with its bytes, as Unix
// gives them: a JSON array of numbers, under "Unix".
func TestAnswerEnvironmentVariableSetGivesItsBytes(t *testing.T) {
	got, err := Answer(`{"environment-variable": "BAR"}`, lookupFrom(map[string]string{"BAR": "ab"}))
	if err != nil || got != `{"environment-variable":{"Unix":[97,98]}}` {
		t.Errorf("Answer = %q, %v; want the bytes of ab", got, err)
	}
}

// A variable that is not set is answered with null.
func TestAnswerEnvironmentVariableMissingIsNull(t *testing.T) {
	got, err := Answer(`{"environment-variable": "NOPE"}`, lookupFrom(nil))
	if err != nil || got != `{"environment-variable":null}` {
		t.Errorf("Answer = %q, %v; want null", got, err)
	}
}

// A set variable with an empty value is an empty array, not null.
func TestAnswerEmptyValueIsAnEmptyArray(t *testing.T) {
	got, err := Answer(`{"environment-variable": "E"}`, lookupFrom(map[string]string{"E": ""}))
	if err != nil || got != `{"environment-variable":{"Unix":[]}}` {
		t.Errorf("Answer = %q, %v; want an empty array", got, err)
	}
}

// Input that is not JSON, or is a request this program does not answer, is an
// error.
func TestAnswerRefusesWhatItDoesNotUnderstand(t *testing.T) {
	for _, in := range []string{`{`, `{"other": 1}`, `[]`} {
		if _, err := Answer(in, lookupFrom(nil)); err == nil {
			t.Errorf("Answer(%q) gave no error", in)
		}
	}
}
