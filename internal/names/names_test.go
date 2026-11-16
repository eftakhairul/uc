package names

import (
	"strings"
	"testing"
)

func TestBareName(t *testing.T) {
	for in, want := range map[string]string{
		"killport":    "killport",
		"killport.sh": "killport",
		"x.py":        "x",
		"x.txt":       "x.txt",
		"a.b.rb":      "a.b",
	} {
		if got := BareName(in); got != want {
			t.Errorf("BareName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeName(t *testing.T) {
	for _, name := range []string{"killport", "killport.sh", "a b", "-odd", "x_1"} {
		if !SafeName(name) {
			t.Errorf("SafeName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", ".", "..", "../x", "a/b", `a\b`, ".DS_Store", ".x.tmp-1", "a\x00b", "/abs"} {
		if SafeName(name) {
			t.Errorf("SafeName(%q) = true, want false", name)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, name := range []string{"killport", "killport.sh", "x", "_x", "9lives", "a-b_c.d", strings.Repeat("a", MaxLen)} {
		if err := ValidName(name); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", name, err)
		}
	}
	for name, wantSub := range map[string]string{
		"":                            "invalid name",
		"a b":                         "invalid name 'a b'",
		"-x":                          "invalid name",
		".x":                          "invalid name",
		"a/b":                         "invalid name",
		"../x":                        "invalid name",
		"a\nb":                        "invalid name",
		"ünï":                         "invalid name",
		strings.Repeat("a", MaxLen+1): "at most 64",
		"list":                        "'list' is a reserved uc command",
		"history.sh":                  "'history' is a reserved uc command",
		"import":                      "reserved",
	} {
		err := ValidName(name)
		if err == nil || !strings.Contains(err.Error(), wantSub) {
			t.Errorf("ValidName(%q) = %v, want an error containing %q", name, err, wantSub)
		}
	}
}

func TestCleanMeta(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":                   "plain text",
		"\x1b[31mRED\x1b[0m":           "[31mRED[0m",
		"a\tb\nc\rd":                   "abcd",
		"del\x7f and c1 \u0085 \u009b": "del and c1  ",
		"bad \xff utf8":                "bad  utf8",
		"emoji 🚀 ok":                   "emoji 🚀 ok",
	} {
		if got := CleanMeta(in); got != want {
			t.Errorf("CleanMeta(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", MaxMetaRunes+50)
	if got := CleanMeta(long); len([]rune(got)) != MaxMetaRunes {
		t.Errorf("CleanMeta(long) has %d runes, want %d", len([]rune(got)), MaxMetaRunes)
	}
}

func TestEscapeControl(t *testing.T) {
	for in, want := range map[string]string{
		"echo hi":        "echo hi",
		"a\nb\tc":        "a\nb\tc",
		"\x1b[31mred":    `\x1b[31mred`,
		"x\x00y\x7f":     `x\x00y\x7f`,
		"c1 \u009b here": `c1 \u009b here`,
	} {
		if got := EscapeControl(in); got != want {
			t.Errorf("EscapeControl(%q) = %q, want %q", in, got, want)
		}
	}
}
