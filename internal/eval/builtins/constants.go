package builtins

// Builtin constants are names that every justfile can use without assigning
// them: the hex digits, the path separators, and the ANSI codes that clear the
// screen, set a text style, or set a colour. Each has a value on Unix and, for
// the two path separators, a different value on Windows. A justfile may assign
// a constant's name, and the assignment wins. The table is read-only.

// constantValue is a constant's value on Unix, and on Windows when that differs
// from Unix; an empty windows value means the same on both.
type constantValue struct {
	unix, windows string
}

var constants = map[string]constantValue{
	"HEX":           {unix: "0123456789abcdef"},
	"HEXLOWER":      {unix: "0123456789abcdef"},
	"HEXUPPER":      {unix: "0123456789ABCDEF"},
	"PATH_SEP":      {unix: "/", windows: `\`},
	"PATH_VAR_SEP":  {unix: ":", windows: ";"},
	"CLEAR":         {unix: "\x1bc"},
	"NORMAL":        {unix: "\x1b[0m"},
	"BOLD":          {unix: "\x1b[1m"},
	"ITALIC":        {unix: "\x1b[3m"},
	"UNDERLINE":     {unix: "\x1b[4m"},
	"INVERT":        {unix: "\x1b[7m"},
	"HIDE":          {unix: "\x1b[8m"},
	"STRIKETHROUGH": {unix: "\x1b[9m"},
	"BLACK":         {unix: "\x1b[30m"},
	"RED":           {unix: "\x1b[31m"},
	"GREEN":         {unix: "\x1b[32m"},
	"YELLOW":        {unix: "\x1b[33m"},
	"BLUE":          {unix: "\x1b[34m"},
	"MAGENTA":       {unix: "\x1b[35m"},
	"CYAN":          {unix: "\x1b[36m"},
	"WHITE":         {unix: "\x1b[37m"},
	"BG_BLACK":      {unix: "\x1b[40m"},
	"BG_RED":        {unix: "\x1b[41m"},
	"BG_GREEN":      {unix: "\x1b[42m"},
	"BG_YELLOW":     {unix: "\x1b[43m"},
	"BG_BLUE":       {unix: "\x1b[44m"},
	"BG_MAGENTA":    {unix: "\x1b[45m"},
	"BG_CYAN":       {unix: "\x1b[46m"},
	"BG_WHITE":      {unix: "\x1b[47m"},
}

// IsConstant reports whether name is a builtin constant. It is pure.
func IsConstant(name string) bool {
	_, ok := constants[name]
	return ok
}

// ConstantValue is the value of the builtin constant name on the system goos,
// and whether there is such a constant. It is pure.
func ConstantValue(name, goos string) (string, bool) {
	c, ok := constants[name]
	if !ok {
		return "", false
	}
	if goos == "windows" && c.windows != "" {
		return c.windows, true
	}
	return c.unix, true
}

// ConstantsFor returns every builtin constant with its value on the system
// goos. The map is new, so the caller may add to it. It is pure.
func ConstantsFor(goos string) map[string]string {
	out := make(map[string]string, len(constants))
	for name := range constants {
		v, _ := ConstantValue(name, goos)
		out[name] = v
	}
	return out
}
