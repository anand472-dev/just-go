package builtins

import (
	"testing"
	"unicode"
)

// caseRows holds, for each input, what each case function returns. Every row
// was produced by oracle/bin/just v1.59.0 with `just --evaluate x` on
// `x := f("<input>")`. The first rows are the cases of the heck crate's own
// tests, which upstream uses for its case functions.
var caseRows = []struct {
	in                                                                    string
	kebab, snake, shoutyKebab, shoutySnake, lowerCamel, upperCamel, title string
	capitalize, lower, upper                                              string
}{
	{"CamelCase", "camel-case", "camel_case", "CAMEL-CASE", "CAMEL_CASE", "camelCase", "CamelCase", "Camel Case", "Camelcase", "camelcase", "CAMELCASE"},
	{"This is Human case.", "this-is-human-case", "this_is_human_case", "THIS-IS-HUMAN-CASE", "THIS_IS_HUMAN_CASE", "thisIsHumanCase", "ThisIsHumanCase", "This Is Human Case", "This is human case.", "this is human case.", "THIS IS HUMAN CASE."},
	{"MixedUP CamelCase, with some Spaces", "mixed-up-camel-case-with-some-spaces", "mixed_up_camel_case_with_some_spaces", "MIXED-UP-CAMEL-CASE-WITH-SOME-SPACES", "MIXED_UP_CAMEL_CASE_WITH_SOME_SPACES", "mixedUpCamelCaseWithSomeSpaces", "MixedUpCamelCaseWithSomeSpaces", "Mixed Up Camel Case With Some Spaces", "Mixedup camelcase, with some spaces", "mixedup camelcase, with some spaces", "MIXEDUP CAMELCASE, WITH SOME SPACES"},
	{"mixed_up_ snake_case with some _spaces", "mixed-up-snake-case-with-some-spaces", "mixed_up_snake_case_with_some_spaces", "MIXED-UP-SNAKE-CASE-WITH-SOME-SPACES", "MIXED_UP_SNAKE_CASE_WITH_SOME_SPACES", "mixedUpSnakeCaseWithSomeSpaces", "MixedUpSnakeCaseWithSomeSpaces", "Mixed Up Snake Case With Some Spaces", "Mixed_up_ snake_case with some _spaces", "mixed_up_ snake_case with some _spaces", "MIXED_UP_ SNAKE_CASE WITH SOME _SPACES"},
	{"kebab-case", "kebab-case", "kebab_case", "KEBAB-CASE", "KEBAB_CASE", "kebabCase", "KebabCase", "Kebab Case", "Kebab-case", "kebab-case", "KEBAB-CASE"},
	{"SHOUTY_SNAKE_CASE", "shouty-snake-case", "shouty_snake_case", "SHOUTY-SNAKE-CASE", "SHOUTY_SNAKE_CASE", "shoutySnakeCase", "ShoutySnakeCase", "Shouty Snake Case", "Shouty_snake_case", "shouty_snake_case", "SHOUTY_SNAKE_CASE"},
	{"snake_case", "snake-case", "snake_case", "SNAKE-CASE", "SNAKE_CASE", "snakeCase", "SnakeCase", "Snake Case", "Snake_case", "snake_case", "SNAKE_CASE"},
	{"this-contains_ ALLKinds OfWord_Boundaries", "this-contains-all-kinds-of-word-boundaries", "this_contains_all_kinds_of_word_boundaries", "THIS-CONTAINS-ALL-KINDS-OF-WORD-BOUNDARIES", "THIS_CONTAINS_ALL_KINDS_OF_WORD_BOUNDARIES", "thisContainsAllKindsOfWordBoundaries", "ThisContainsAllKindsOfWordBoundaries", "This Contains All Kinds Of Word Boundaries", "This-contains_ allkinds ofword_boundaries", "this-contains_ allkinds ofword_boundaries", "THIS-CONTAINS_ ALLKINDS OFWORD_BOUNDARIES"},
	{"XΣXΣ baﬄe", "xσxς-baﬄe", "xσxς_baﬄe", "XΣXΣ-BAFFLE", "XΣXΣ_BAFFLE", "xσxςBaﬄe", "XσxςBaﬄe", "Xσxς Baﬄe", "Xσxσ baﬄe", "xσxς baﬄe", "XΣXΣ BAFFLE"},
	{"XMLHttpRequest", "xml-http-request", "xml_http_request", "XML-HTTP-REQUEST", "XML_HTTP_REQUEST", "xmlHttpRequest", "XmlHttpRequest", "Xml Http Request", "Xmlhttprequest", "xmlhttprequest", "XMLHTTPREQUEST"},
	{"ل\u0650ن\u064Eذ\u0652ه\u064Eب\u0652 إ\u0650ل\u064Eى الس\u0650\u0651ي\u0652ن\u064Eم\u064Eا", "ل\u0650ن\u064Eذ\u0652ه\u064Eب\u0652-إ\u0650ل\u064Eى-الس\u0650\u0651ي\u0652ن\u064Eم\u064Eا", "ل\u0650ن\u064Eذ\u0652ه\u064Eب\u0652_إ\u0650ل\u064Eى_الس\u0650\u0651ي\u0652ن\u064Eم\u064Eا", "ل\u0650ن\u064Eذ\u0652ه\u064Eب\u0652-إ\u0650ل\u064Eى-الس\u0650\u0651ي\u0652ن\u064Eم\u064Eا", "ل\u0650ن\u064Eذ\u0652ه\u064Eب\u0652_إ\u0650ل\u064Eى_الس\u0650\u0651ي\u0652ن\u064Eم\u064Eا", "ل\u0650ن\u064Eذ\u0652ه\u064Eب\u0652إ\u0650ل\u064Eىالس\u0650\u0651ي\u0652ن\u064Eم\u064Eا", "ل\u0650ن\u064Eذ\u0652ه\u064Eب\u0652إ\u0650ل\u064Eىالس\u0650\u0651ي\u0652ن\u064Eم\u064Eا", "ل\u0650ن\u064Eذ\u0652ه\u064Eب\u0652 إ\u0650ل\u064Eى الس\u0650\u0651ي\u0652ن\u064Eم\u064Eا", "ل\u0650ن\u064Eذ\u0652ه\u064Eب\u0652 إ\u0650ل\u064Eى الس\u0650\u0651ي\u0652ن\u064Eم\u064Eا", "ل\u0650ن\u064Eذ\u0652ه\u064Eب\u0652 إ\u0650ل\u064Eى الس\u0650\u0651ي\u0652ن\u064Eم\u064Eا", "ل\u0650ن\u064Eذ\u0652ه\u064Eب\u0652 إ\u0650ل\u064Eى الس\u0650\u0651ي\u0652ن\u064Eم\u064Eا"},
	{"ファイルを読み込み", "ファイルを読み込み", "ファイルを読み込み", "ファイルを読み込み", "ファイルを読み込み", "ファイルを読み込み", "ファイルを読み込み", "ファイルを読み込み", "ファイルを読み込み", "ファイルを読み込み", "ファイルを読み込み"},
	{"祝你一天过得愉快", "祝你一天过得愉快", "祝你一天过得愉快", "祝你一天过得愉快", "祝你一天过得愉快", "祝你一天过得愉快", "祝你一天过得愉快", "祝你一天过得愉快", "祝你一天过得愉快", "祝你一天过得愉快", "祝你一天过得愉快"},
	{"FIELD_NAME11", "field-name11", "field_name11", "FIELD-NAME11", "FIELD_NAME11", "fieldName11", "FieldName11", "Field Name11", "Field_name11", "field_name11", "FIELD_NAME11"},
	{"99BOTTLES", "99bottles", "99bottles", "99BOTTLES", "99BOTTLES", "99bottles", "99bottles", "99bottles", "99bottles", "99bottles", "99BOTTLES"},
	{"FieldNamE11", "field-nam-e11", "field_nam_e11", "FIELD-NAM-E11", "FIELD_NAM_E11", "fieldNamE11", "FieldNamE11", "Field Nam E11", "Fieldname11", "fieldname11", "FIELDNAME11"},
	{"abc123def456", "abc123def456", "abc123def456", "ABC123DEF456", "ABC123DEF456", "abc123def456", "Abc123def456", "Abc123def456", "Abc123def456", "abc123def456", "ABC123DEF456"},
	{"abc123DEF456", "abc123-def456", "abc123_def456", "ABC123-DEF456", "ABC123_DEF456", "abc123Def456", "Abc123Def456", "Abc123 Def456", "Abc123def456", "abc123def456", "ABC123DEF456"},
	{"abc123Def456", "abc123-def456", "abc123_def456", "ABC123-DEF456", "ABC123_DEF456", "abc123Def456", "Abc123Def456", "Abc123 Def456", "Abc123def456", "abc123def456", "ABC123DEF456"},
	{"abc123DEf456", "abc123-d-ef456", "abc123_d_ef456", "ABC123-D-EF456", "ABC123_D_EF456", "abc123DEf456", "Abc123DEf456", "Abc123 D Ef456", "Abc123def456", "abc123def456", "ABC123DEF456"},
	{"ABC123def456", "abc123def456", "abc123def456", "ABC123DEF456", "ABC123DEF456", "abc123def456", "Abc123def456", "Abc123def456", "Abc123def456", "abc123def456", "ABC123DEF456"},
	{"ABC123DEF456", "abc123def456", "abc123def456", "ABC123DEF456", "ABC123DEF456", "abc123def456", "Abc123def456", "Abc123def456", "Abc123def456", "abc123def456", "ABC123DEF456"},
	{"ABC123Def456", "abc123-def456", "abc123_def456", "ABC123-DEF456", "ABC123_DEF456", "abc123Def456", "Abc123Def456", "Abc123 Def456", "Abc123def456", "abc123def456", "ABC123DEF456"},
	{"", "", "", "", "", "", "", "", "", "", ""},
	{"  ", "", "", "", "", "", "", "", "  ", "  ", "  "},
	{"__--", "", "", "", "", "", "", "", "__--", "__--", "__--"},
	{"a", "a", "a", "A", "A", "a", "A", "A", "A", "a", "A"},
	{"A", "a", "a", "A", "A", "a", "A", "A", "A", "a", "A"},
	{"aB", "a-b", "a_b", "A-B", "A_B", "aB", "AB", "A B", "Ab", "ab", "AB"},
	{"Ab", "ab", "ab", "AB", "AB", "ab", "Ab", "Ab", "Ab", "ab", "AB"},
	{"ß straße", "ß-straße", "ß_straße", "SS-STRASSE", "SS_STRASSE", "ßStraße", "SSStraße", "SS Straße", "SS straße", "ß straße", "SS STRASSE"},
	{"İstanbul", "i\u0307stanbul", "i\u0307stanbul", "İSTANBUL", "İSTANBUL", "i\u0307stanbul", "İstanbul", "İstanbul", "İstanbul", "i\u0307stanbul", "İSTANBUL"},
	{"ǅungla", "ǆungla", "ǆungla", "ǄUNGLA", "ǄUNGLA", "ǆungla", "Ǆungla", "Ǆungla", "Ǆungla", "ǆungla", "ǄUNGLA"},
	{"ΑΣ ΑΣ.", "ας-ας", "ας_ας", "ΑΣ-ΑΣ", "ΑΣ_ΑΣ", "αςΑς", "ΑςΑς", "Ας Ας", "Ασ ασ.", "ας ας.", "ΑΣ ΑΣ."},
	{"ΑΣΣ", "ασς", "ασς", "ΑΣΣ", "ΑΣΣ", "ασς", "Ασς", "Ασς", "Ασσ", "ασς", "ΑΣΣ"},
	{"Σ", "ς", "ς", "Σ", "Σ", "ς", "Σ", "Σ", "Σ", "σ", "Σ"},
	{"foo.bar", "foo-bar", "foo_bar", "FOO-BAR", "FOO_BAR", "fooBar", "FooBar", "Foo Bar", "Foo.bar", "foo.bar", "FOO.BAR"},
	{"tab\tsep\nnl", "tab-sep-nl", "tab_sep_nl", "TAB-SEP-NL", "TAB_SEP_NL", "tabSepNl", "TabSepNl", "Tab Sep Nl", "Tab\tsep\nnl", "tab\tsep\nnl", "TAB\tSEP\nNL"},
	{"éÉ ÀB", "é-é-àb", "é_é_àb", "É-É-ÀB", "É_É_ÀB", "éÉÀb", "ÉÉÀb", "É É Àb", "Éé àb", "éé àb", "ÉÉ ÀB"},
	{"1a2B3c", "1a2-b3c", "1a2_b3c", "1A2-B3C", "1A2_B3C", "1a2B3c", "1a2B3c", "1a2 B3c", "1a2b3c", "1a2b3c", "1A2B3C"},
}

func TestCaseFunctionsMatchUpstream(t *testing.T) {
	funcs := []struct {
		name string
		f    func(string) string
		got  func(i int) string
	}{
		{"kebabcase", kebabcase, func(i int) string { return caseRows[i].kebab }},
		{"snakecase", snakecase, func(i int) string { return caseRows[i].snake }},
		{"shoutykebabcase", shoutykebabcase, func(i int) string { return caseRows[i].shoutyKebab }},
		{"shoutysnakecase", shoutysnakecase, func(i int) string { return caseRows[i].shoutySnake }},
		{"lowercamelcase", lowercamelcase, func(i int) string { return caseRows[i].lowerCamel }},
		{"uppercamelcase", uppercamelcase, func(i int) string { return caseRows[i].upperCamel }},
		{"titlecase", titlecase, func(i int) string { return caseRows[i].title }},
		{"capitalize", capitalize, func(i int) string { return caseRows[i].capitalize }},
		{"lowercase", lowercase, func(i int) string { return caseRows[i].lower }},
		{"uppercase", uppercase, func(i int) string { return caseRows[i].upper }},
	}
	for _, fn := range funcs {
		for i, row := range caseRows {
			if got, want := fn.f(row.in), fn.got(i); got != want {
				t.Errorf("%s(%q) = %q, want %q", fn.name, row.in, got, want)
			}
		}
	}
}

// Rust lowercases a capital sigma to the final form when a cased letter comes
// before it, with nothing cased after, ignoring the case-ignorable characters
// in between (str::to_lowercase). The rows were produced by oracle/bin/just.
func TestLowercaseChoosesTheFinalSigmaAsRustDoes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Σ", "σ"}, {"AΣ", "aς"}, {"AΣA", "aσa"}, {"A.Σ", "a.ς"}, {"AΣ.", "aς."}, {"AΣ.A", "aσ.a"},
		{"AΣ\u0301", "aς\u0301"}, {"ΑΣ ΑΣ", "ας ας"}, {"1Σ", "1σ"}, {"Σ1", "σ1"},
	} {
		if got := lowercase(tc.in); got != tc.want {
			t.Errorf("lowercase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The Unicode properties the case functions read are Rust's: they were
// compared with char::is_alphanumeric, is_lowercase and is_uppercase over
// every code point when the functions were written. These spot checks keep
// the cases that differ from Go's own predicates.
func TestRustCharacterProperties(t *testing.T) {
	for _, tc := range []struct {
		r                   rune
		alnum, lower, upper bool
	}{
		{'a', true, true, false}, {'A', true, false, true}, {'7', true, false, false}, {'_', false, false, false},
		{'\u0650', true, false, false}, // Arabic kasra: a mark, but Other_Alphabetic
		{'\u00AA', true, true, false},  // feminine ordinal indicator: Lo, Other_Lowercase
		{'\u2160', true, false, true},  // roman numeral one: Nl, Other_Uppercase
		{'\u00B2', true, false, false}, // superscript two: No
		{' ', false, false, false}, {'-', false, false, false},
	} {
		if got := isAlphanumeric(tc.r); got != tc.alnum {
			t.Errorf("isAlphanumeric(%U) = %v", tc.r, got)
		}
		if got := isLowercase(tc.r); got != tc.lower {
			t.Errorf("isLowercase(%U) = %v", tc.r, got)
		}
		if got := isUppercase(tc.r); got != tc.upper {
			t.Errorf("isUppercase(%U) = %v", tc.r, got)
		}
	}
}

func TestCaseIgnorableAndCasedCharacters(t *testing.T) {
	for _, r := range []rune{'\'', '.', ':', '\u00B7', '\u2019', '\u0301', '\u00AD', '\u02B0', '^'} {
		if !isCaseIgnorable(r) {
			t.Errorf("isCaseIgnorable(%U) = false", r)
		}
	}
	for _, r := range []rune{'a', '1', ' ', 'Σ'} {
		if isCaseIgnorable(r) {
			t.Errorf("isCaseIgnorable(%U) = true", r)
		}
	}
	for _, r := range []rune{'a', 'A', 'ǅ', '\u02B0'} {
		if !isCased(r) {
			t.Errorf("isCased(%U) = false", r)
		}
	}
	for _, r := range []rune{'1', ' ', '.'} {
		if isCased(r) {
			t.Errorf("isCased(%U) = true", r)
		}
	}
}

func BenchmarkCaseFunctions(b *testing.B) {
	for _, bench := range []struct {
		name string
		f    func(string) string
	}{
		{"kebabcase", kebabcase}, {"uppercamelcase", uppercamelcase}, {"titlecase", titlecase},
		{"lowercase", lowercase}, {"uppercase", uppercase}, {"capitalize", capitalize},
	} {
		b.Run(bench.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				bench.f("MixedUP CamelCase, with some Spaces and XMLHttpRequest")
			}
		})
	}
}

// The case tables were generated from the standard library of the Rust
// compiler that built the oracle (Unicode 17), and Go's unicode package supplies
// the rest. When Go's tables move to another Unicode version, the two may
// differ for a few code points, and this test says so.
func TestGoUnicodeVersionMatchesTheOracleToolchain(t *testing.T) {
	if unicode.Version != unicodeVersion {
		t.Errorf("unicode.Version = %s, the oracle's rustc has Unicode %s: regenerate casetable.go and check the splitting tables",
			unicode.Version, unicodeVersion)
	}
}
