package timefmt

import (
	"fmt"

	"gitea.com/wimwian/just-go/internal/sys"
)

// civil is a time broken down into the fields strftime prints, in the local
// zone of the time it was made from.
type civil struct {
	year, month, day int
	ordinal          int // day of the year, from 1
	weekday          int // from 0 for Sunday
	hour, min, sec   int
	nano             int
	unix             int64
	offset           int
	utc              bool
	isoYear, isoWeek int
}

// newCivil breaks t down. Pure.
func newCivil(t sys.Time) civil {
	local := t.Unix + int64(t.Offset)
	days := floorDiv(local, 86400)
	secs := int(local - days*86400)
	year, month, day := civilFromDays(days)
	c := civil{
		year: year, month: month, day: day,
		weekday: int(floorMod(days+4, 7)),
		hour:    secs / 3600, min: secs / 60 % 60, sec: secs % 60,
		nano: t.Nano, unix: t.Unix, offset: t.Offset, utc: t.UTC,
	}
	c.ordinal = int(days-daysFromCivil(year, 1, 1)) + 1
	c.isoYear, c.isoWeek = isoWeek(year, c.ordinal, c.weekday)
	return c
}

// floorDiv is a/b rounded toward negative infinity, for positive b. Pure.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b < 0 {
		q--
	}
	return q
}

// floorMod is a modulo b, never negative, for positive b. Pure.
func floorMod(a, b int64) int64 {
	return a - floorDiv(a, b)*b
}

// daysFromCivil is the number of days from 1970-01-01 to the date, in the
// proleptic Gregorian calendar. Pure.
func daysFromCivil(year, month, day int) int64 {
	y := int64(year)
	if month <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	m := int64(month)
	shifted := m - 3
	if m <= 2 {
		shifted = m + 9
	}
	doy := (153*shifted+2)/5 + int64(day) - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// civilFromDays is the date days after 1970-01-01. Pure.
func civilFromDays(days int64) (year, month, day int) {
	z := days + 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := int(doy - (153*mp+2)/5 + 1)
	m := int(mp) + 3
	if m > 12 {
		m -= 12
	}
	y := int(yoe + era*400)
	if m <= 2 {
		y++
	}
	return y, m, d
}

// isoWeek is the ISO 8601 year and week of the date with the given year, day
// of the year and weekday (0 for Sunday). Pure.
func isoWeek(year, ordinal, weekday int) (int, int) {
	iso := weekday
	if iso == 0 {
		iso = 7
	}
	week := (ordinal - iso + 10) / 7
	switch {
	case week < 1:
		return year - 1, weeksInYear(year - 1)
	case week > weeksInYear(year):
		return year + 1, 1
	}
	return year, week
}

// weeksInYear is 52 or 53, the number of ISO weeks of year. Pure.
func weeksInYear(year int) int {
	if isoLong(year) {
		return 53
	}
	return 52
}

// isoLong reports whether year has 53 ISO weeks: it starts on a Thursday, or
// is a leap year that starts on a Wednesday. Pure.
func isoLong(year int) bool {
	start := int(floorMod(daysFromCivil(year, 1, 1)+4, 7))
	return start == 4 || (start == 3 && leap(year))
}

// leap reports whether year is a leap year of the Gregorian calendar. Pure.
func leap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// meridiem is "AM" or "PM". Pure.
func (c civil) meridiem() string {
	if c.hour >= 12 {
		return "PM"
	}
	return "AM"
}

// hour12 is the hour on a twelve hour clock, from 1 to 12. Pure.
func (c civil) hour12() int {
	if h := c.hour % 12; h != 0 {
		return h
	}
	return 12
}

// zone is the name of the time zone as chrono prints it: "UTC", or the
// offset from UTC, with seconds only when it has some. Pure.
func (c civil) zone() string {
	if c.utc {
		return "UTC"
	}
	sign, offset := "+", c.offset
	if offset < 0 {
		sign, offset = "-", -offset
	}
	name := fmt.Sprintf("%s%02d:%02d", sign, offset/3600, offset/60%60)
	if sec := offset % 60; sec > 0 {
		name += fmt.Sprintf(":%02d", sec)
	}
	return name
}

// style is how a numeric specifier writes its value.
type style int

const (
	// twoDigits writes at least two digits, padded by the item's padding.
	twoDigits style = iota
	// oneDigit writes the value as it is.
	oneDigit
	// wide writes the value padded to a given width.
	wide
	// yearStyle writes a year: four digits, with a sign outside 0 to 9999.
	yearStyle
)

// numeric is a specifier that prints a number.
type numeric struct {
	style style
	width int
	pad   pad
	value func(c civil) int64
}

// format writes the value of n for c with padding p. Pure.
func (n *numeric) format(c civil, p pad) string {
	v := n.value(c)
	switch n.style {
	case oneDigit:
		return fmt.Sprint(v)
	case twoDigits:
		return padded(v, 2, p, false)
	case yearStyle:
		return padded(v, 4, p, v < 0 || v > 9999)
	}
	return padded(v, n.width, p, false)
}

// padded writes v in at least width characters, padded as p says and with a
// sign when sign is set, as chrono's write_n does. Pure.
func padded(v int64, width int, p pad, sign bool) string {
	if sign {
		width++
	}
	flags := ""
	if sign {
		flags = "+"
	}
	switch p {
	case padZero:
		return fmt.Sprintf("%"+flags+"0*d", width, v)
	case padSpace:
		return fmt.Sprintf("%"+flags+"*d", width, v)
	}
	return fmt.Sprintf("%"+flags+"d", v)
}

// numerics are the specifiers that print a number.
var numerics = map[byte]*numeric{
	'C': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return floorDiv(int64(c.year), 100) }},
	'G': {style: yearStyle, pad: padZero, value: func(c civil) int64 { return int64(c.isoYear) }},
	'H': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return int64(c.hour) }},
	'I': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return int64(c.hour12()) }},
	'M': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return int64(c.min) }},
	'S': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return int64(c.sec) }},
	'U': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return int64((c.ordinal - c.weekday + 6) / 7) }},
	'V': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return int64(c.isoWeek) }},
	'W': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return int64((c.ordinal - (c.weekday+6)%7 + 6) / 7) }},
	'Y': {style: yearStyle, pad: padZero, value: func(c civil) int64 { return int64(c.year) }},
	'd': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return int64(c.day) }},
	'e': {style: twoDigits, pad: padSpace, value: func(c civil) int64 { return int64(c.day) }},
	'f': {style: wide, width: 9, pad: padZero, value: func(c civil) int64 { return int64(c.nano) }},
	'g': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return floorMod(int64(c.isoYear), 100) }},
	'j': {style: wide, width: 3, pad: padZero, value: func(c civil) int64 { return int64(c.ordinal) }},
	'k': {style: twoDigits, pad: padSpace, value: func(c civil) int64 { return int64(c.hour) }},
	'l': {style: twoDigits, pad: padSpace, value: func(c civil) int64 { return int64(c.hour12()) }},
	'm': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return int64(c.month) }},
	'q': {style: oneDigit, pad: padNone, value: func(c civil) int64 { return int64((c.month + 2) / 3) }},
	's': {style: wide, width: 9, pad: padNone, value: func(c civil) int64 { return c.unix }},
	'u': {style: oneDigit, pad: padNone, value: func(c civil) int64 { return int64((c.weekday+6)%7 + 1) }},
	'w': {style: oneDigit, pad: padNone, value: func(c civil) int64 { return int64(c.weekday) }},
	'y': {style: twoDigits, pad: padZero, value: func(c civil) int64 { return floorMod(int64(c.year), 100) }},
}
