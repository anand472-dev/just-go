package timefmt

import (
	"errors"
	"testing"

	"gitea.com/wimwian/just-go/internal/sys"
)

// docTime is 2001-07-08 00:34:59.026490708 at +09:30, the time chrono's own
// documentation formats.
var docTime = sys.Time{Unix: 994518299, Nano: 26490708, Offset: 34200}

func TestFormatMatchesChronoDocumentation(t *testing.T) {
	tests := []struct{ layout, want string }{
		{"%Y", "2001"}, {"%C", "20"}, {"%y", "01"}, {"%q", "3"}, {"%m", "07"}, {"%b", "Jul"},
		{"%B", "July"}, {"%h", "Jul"}, {"%d", "08"}, {"%e", " 8"}, {"%_d", " 8"}, {"%a", "Sun"},
		{"%A", "Sunday"}, {"%w", "0"}, {"%u", "7"}, {"%U", "27"}, {"%W", "27"}, {"%G", "2001"},
		{"%g", "01"}, {"%V", "27"}, {"%j", "189"}, {"%D", "07/08/01"}, {"%x", "07/08/01"},
		{"%F", "2001-07-08"}, {"%v", " 8-Jul-2001"}, {"%H", "00"}, {"%k", " 0"}, {"%_H", " 0"},
		{"%I", "12"}, {"%l", "12"}, {"%_I", "12"}, {"%P", "am"}, {"%p", "AM"}, {"%M", "34"},
		{"%S", "59"}, {"%f", "026490708"}, {"%.f", ".026490708"}, {"%.3f", ".026"},
		{"%.6f", ".026490"}, {"%.9f", ".026490708"}, {"%3f", "026"}, {"%6f", "026490"},
		{"%9f", "026490708"}, {"%R", "00:34"}, {"%T", "00:34:59"}, {"%X", "00:34:59"},
		{"%r", "12:34:59 AM"}, {"%z", "+0930"}, {"%:z", "+09:30"}, {"%::z", "+09:30:00"},
		{"%:::z", "+09"}, {"%Z", "+09:30"}, {"%c", "Sun Jul  8 00:34:59 2001"},
		{"%+", "2001-07-08T00:34:59.026490708+09:30"}, {"%s", "994518299"}, {"%t", "\t"},
		{"%n", "\n"}, {"%%", "%"},
		{"  %Y%d%m%%%%%t%H%M%S\t", "  20010807%%\t003459\t"},
		{"  %Y%d%m%%%%%t%H:%P:%M%S%:::z\t", "  20010807%%\t00:am:3459+09\t"},
		{"", ""}, {"no specifiers, just text", "no specifiers, just text"}, {"é%Yé", "é2001é"},
	}
	for _, tt := range tests {
		got, err := Format(docTime, tt.layout)
		if err != nil || got != tt.want {
			t.Errorf("Format(%q) = %q, %v; want %q", tt.layout, got, err, tt.want)
		}
	}
}

func TestFormatPaddingOverrides(t *testing.T) {
	tests := []struct{ layout, want string }{
		{"%-d", "8"}, {"%-m", "7"}, {"%-H", "0"}, {"%-j", "189"}, {"%_j", "189"}, {"%0e", "08"},
		{"%-e", "8"}, {"%-I", "12"}, {"%_m", " 7"}, {"%0k", "00"}, {"%-f", "26490708"},
		{"%_f", " 26490708"}, {"%-s", "994518299"}, {"%-y", "1"}, {"%_y", " 1"}, {"%-Y", "2001"},
		{"%-w", "0"}, {"%-q", "3"},
	}
	for _, tt := range tests {
		got, err := Format(docTime, tt.layout)
		if err != nil || got != tt.want {
			t.Errorf("Format(%q) = %q, %v; want %q", tt.layout, got, err, tt.want)
		}
	}
	short := sys.Time{Unix: 31 * 86400}
	for layout, want := range map[string]string{"%-j": "32", "%_j": " 32", "%j": "032", "%0j": "032"} {
		if got, _ := Format(short, layout); got != want {
			t.Errorf("Format(%q) = %q; want %q", layout, got, want)
		}
	}
}

func TestFormatFractionsOfASecond(t *testing.T) {
	tests := []struct {
		nano   int
		layout string
		want   string
	}{
		{0, "%.f", ""}, {500_000_000, "%.f", ".500"}, {26_490_000, "%.f", ".026490"},
		{26_000_000, "%.f", ".026"}, {1, "%.f", ".000000001"}, {0, "%.3f", ".000"},
		{7_000, "%f", "000007000"}, {7_000, "%.6f", ".000007"}, {7_000, "%6f", "000007"},
		{999_999_999, "%.3f", ".999"}, {999_999_999, "%3f", "999"}, {5, "%9f", "000000005"},
		{5, "%.9f", ".000000005"},
	}
	for _, tt := range tests {
		got, err := Format(sys.Time{Nano: tt.nano}, tt.layout)
		if err != nil || got != tt.want {
			t.Errorf("Format(nano %d, %q) = %q, %v; want %q", tt.nano, tt.layout, got, err, tt.want)
		}
	}
}

func TestFormatZones(t *testing.T) {
	tests := []struct {
		at     sys.Time
		layout string
		want   string
	}{
		{sys.Time{UTC: true}, "%Z", "UTC"},
		{sys.Time{UTC: true}, "%z %:z %::z %:::z", "+0000 +00:00 +00:00:00 +00"},
		{sys.Time{Offset: -18000}, "%z %:z %Z", "-0500 -05:00 -05:00"},
		{sys.Time{Offset: 3600 + 45}, "%z %:z %::z %Z", "+0101 +01:01 +01:00:45 +01:00:45"},
		{sys.Time{Offset: 3600 + 29}, "%z", "+0100"},
		{sys.Time{Offset: -(3600 + 29)}, "%:z", "-01:00"},
		{sys.Time{Offset: 5*3600 + 1800}, "%:::z", "+05"},
		{sys.Time{Offset: 36000}, "%+", "1970-01-01T10:00:00+10:00"},
		{sys.Time{Unix: 3600, Nano: 500_000_000, Offset: -7200}, "%+", "1969-12-31T23:00:00.500-02:00"},
	}
	for _, tt := range tests {
		got, err := Format(tt.at, tt.layout)
		if err != nil || got != tt.want {
			t.Errorf("Format(%+v, %q) = %q, %v; want %q", tt.at, tt.layout, got, err, tt.want)
		}
	}
}

func TestFormatYearsOutsideFourDigits(t *testing.T) {
	tests := []struct {
		year   int
		layout string
		want   string
	}{
		{999, "%Y", "0999"}, {5, "%Y", "0005"}, {10000, "%Y", "+10000"}, {-5, "%Y", "-0005"},
		{999, "%-Y", "999"}, {999, "%_Y", " 999"}, {10000, "%-Y", "+10000"}, {10000, "%_Y", "+10000"},
		{-5, "%_Y", "   -5"}, {-5, "%y", "95"},
	}
	for _, tt := range tests {
		at := sys.Time{Unix: daysFromCivil(tt.year, 6, 15) * 86400}
		got, err := Format(at, tt.layout)
		if err != nil || got != tt.want {
			t.Errorf("Format(year %d, %q) = %q, %v; want %q", tt.year, tt.layout, got, err, tt.want)
		}
	}
}

func TestFormatRejectsWhatChronoRejects(t *testing.T) {
	for _, layout := range []string{
		"%", "%%%", "%%a%", "%Q", "%?", "%42", "% +", "%.Z", "%:Z", "%-Z", "%0Z", "%_Z", "%.j", "%:j",
		"%#m", "%-a", "%_B", "%-D", "%0F", "%-T", "%-+", "%.", "%.3", "%.4f", "%.x", "%3", "%3x", "%6",
		"%9y", "%:", "%::", "%:::", "%:y", "%-", "%0", "%#", "%-#z", "ok %Q", "%😽", "%-😽",
	} {
		_, err := Format(docTime, layout)
		var e *Error
		if !errors.As(err, &e) || !e.Parse {
			t.Errorf("Format(%q) error = %v; want a parse error", layout, err)
		}
	}
}

func TestErrorWordsFailuresAsUpstream(t *testing.T) {
	_, err := Format(docTime, "%Q")
	want := "failed to parse time format string `%Q`: bad or unsupported format string"
	if err == nil || err.Error() != want {
		t.Errorf("Format(%%Q) error = %v; want %q", err, want)
	}
	_, err = Format(docTime, "%#z")
	want = "failed to format time with format string `%#z`"
	if err == nil || err.Error() != want {
		t.Errorf("Format(%%#z) error = %v; want %q", err, want)
	}
}

func TestFormatChecksTheWholeLayoutBeforeFormatting(t *testing.T) {
	_, err := Format(docTime, "%Y-%#z-%Q")
	var e *Error
	if !errors.As(err, &e) || !e.Parse {
		t.Errorf("error = %v; want the parse error of the later %%Q", err)
	}
}

func TestFormatCalendarFieldsMatchTheCLibrary(t *testing.T) {
	const layout = "%Y-%m-%d %H:%M:%S|%a|%A|%b|%B|%j|%U|%W|%V|%G|%g|%u|%w|%y|%C|%I|%p|%e|%k|%l"
	tests := []struct {
		unix int64
		want string
	}{
		{0, "1970-01-01 00:00:00|Thu|Thursday|Jan|January|001|00|00|01|1970|70|4|4|70|19|12|AM| 1| 0|12"},
		{-1, "1969-12-31 23:59:59|Wed|Wednesday|Dec|December|365|52|52|01|1970|70|3|3|69|19|11|PM|31|23|11"},
		{86399, "1970-01-01 23:59:59|Thu|Thursday|Jan|January|001|00|00|01|1970|70|4|4|70|19|11|PM| 1|23|11"},
		{951782400, "2000-02-29 00:00:00|Tue|Tuesday|Feb|February|060|09|09|09|2000|00|2|2|00|20|12|AM|29| 0|12"},
		{1009843200, "2002-01-01 00:00:00|Tue|Tuesday|Jan|January|001|00|00|01|2002|02|2|2|02|20|12|AM| 1| 0|12"},
		{1230768000, "2009-01-01 00:00:00|Thu|Thursday|Jan|January|001|00|00|01|2009|09|4|4|09|20|12|AM| 1| 0|12"},
		{1262304000, "2010-01-01 00:00:00|Fri|Friday|Jan|January|001|00|00|53|2009|09|5|5|10|20|12|AM| 1| 0|12"},
		{1293753600, "2010-12-31 00:00:00|Fri|Friday|Dec|December|365|52|52|52|2010|10|5|5|10|20|12|AM|31| 0|12"},
		{1325376000, "2012-01-01 00:00:00|Sun|Sunday|Jan|January|001|01|00|52|2011|11|7|0|12|20|12|AM| 1| 0|12"},
		{1356912000, "2012-12-31 00:00:00|Mon|Monday|Dec|December|366|53|53|01|2013|13|1|1|12|20|12|AM|31| 0|12"},
		{1388448000, "2013-12-31 00:00:00|Tue|Tuesday|Dec|December|365|52|52|01|2014|14|2|2|13|20|12|AM|31| 0|12"},
		{1420070400, "2015-01-01 00:00:00|Thu|Thursday|Jan|January|001|00|00|01|2015|15|4|4|15|20|12|AM| 1| 0|12"},
		{1451606400, "2016-01-01 00:00:00|Fri|Friday|Jan|January|001|00|00|53|2015|15|5|5|16|20|12|AM| 1| 0|12"},
		{1483228800, "2017-01-01 00:00:00|Sun|Sunday|Jan|January|001|01|00|52|2016|16|7|0|17|20|12|AM| 1| 0|12"},
		{1514764800, "2018-01-01 00:00:00|Mon|Monday|Jan|January|001|00|01|01|2018|18|1|1|18|20|12|AM| 1| 0|12"},
		{1546300800, "2019-01-01 00:00:00|Tue|Tuesday|Jan|January|001|00|00|01|2019|19|2|2|19|20|12|AM| 1| 0|12"},
		{1577836800, "2020-01-01 00:00:00|Wed|Wednesday|Jan|January|001|00|00|01|2020|20|3|3|20|20|12|AM| 1| 0|12"},
		{1609372800, "2020-12-31 00:00:00|Thu|Thursday|Dec|December|366|52|52|53|2020|20|4|4|20|20|12|AM|31| 0|12"},
		{1609459200, "2021-01-01 00:00:00|Fri|Friday|Jan|January|001|00|00|53|2020|20|5|5|21|20|12|AM| 1| 0|12"},
		{1640995200, "2022-01-01 00:00:00|Sat|Saturday|Jan|January|001|00|00|52|2021|21|6|6|22|20|12|AM| 1| 0|12"},
		{1672531200, "2023-01-01 00:00:00|Sun|Sunday|Jan|January|001|01|00|52|2022|22|7|0|23|20|12|AM| 1| 0|12"},
		{1704067200, "2024-01-01 00:00:00|Mon|Monday|Jan|January|001|00|01|01|2024|24|1|1|24|20|12|AM| 1| 0|12"},
		{1735603200, "2024-12-31 00:00:00|Tue|Tuesday|Dec|December|366|52|53|01|2025|25|2|2|24|20|12|AM|31| 0|12"},
		{1735689600, "2025-01-01 00:00:00|Wed|Wednesday|Jan|January|001|00|00|01|2025|25|3|3|25|20|12|AM| 1| 0|12"},
		{4107542400, "2100-03-01 00:00:00|Mon|Monday|Mar|March|060|09|09|09|2100|00|1|1|00|21|12|AM| 1| 0|12"},
		{4102444800, "2100-01-01 00:00:00|Fri|Friday|Jan|January|001|00|00|53|2099|99|5|5|00|21|12|AM| 1| 0|12"},
		{-31536000, "1969-01-01 00:00:00|Wed|Wednesday|Jan|January|001|00|00|01|1969|69|3|3|69|19|12|AM| 1| 0|12"},
		{-2208988800, "1900-01-01 00:00:00|Mon|Monday|Jan|January|001|00|01|01|1900|00|1|1|00|19|12|AM| 1| 0|12"},
		{951868800, "2000-03-01 00:00:00|Wed|Wednesday|Mar|March|061|09|09|09|2000|00|3|3|00|20|12|AM| 1| 0|12"},
		{1583020800, "2020-03-01 00:00:00|Sun|Sunday|Mar|March|061|09|08|09|2020|20|7|0|20|20|12|AM| 1| 0|12"},
	}
	for _, tt := range tests {
		got, err := Format(sys.Time{Unix: tt.unix, UTC: true}, layout)
		if err != nil || got != tt.want {
			t.Errorf("Format(%d) = %q, %v; want %q", tt.unix, got, err, tt.want)
		}
	}
}

func TestFormatUsesTheLocalDate(t *testing.T) {
	// 2024-12-31 23:30 UTC is 2025-01-01 in a zone one hour east.
	at := sys.Time{Unix: 1735687800, Offset: 3600}
	got, _ := Format(at, "%F %T %G-W%V-%u")
	if want := "2025-01-01 00:30:00 2025-W01-3"; got != want {
		t.Errorf("Format = %q; want %q", got, want)
	}
}

func BenchmarkFormat(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Format(docTime, "%Y-%m-%d %H:%M:%S%.3f %z"); err != nil {
			b.Fatal(err)
		}
	}
}
