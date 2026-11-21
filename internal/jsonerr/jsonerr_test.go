package jsonerr

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDescribe(t *testing.T) {
	type inner struct {
		Hour *float64 `json:"hour"`
	}
	type settings struct {
		Size  *int              `json:"history_size"`
		On    *bool             `json:"enabled"`
		Ed    *string           `json:"editor"`
		Inner inner             `json:"weights"`
		Ex    []string          `json:"examples"`
		M     map[string]string `json:"m"`
	}
	for in, want := range map[string]string{
		"{\n  \"history_size\": 1,\n  oops\n}": "invalid JSON at line 3",
		`{"history_size": "x"}`:                `"history_size" must be a whole number`,
		`{"enabled": 1}`:                       `"enabled" must be true or false`,
		`{"editor": 3}`:                        `"editor" must be text`,
		`{"weights": {"hour": "x"}}`:           `"weights.hour" must be a number`,
		`{"examples": "x"}`:                    `"examples" must be a list`,
		`[1]`:                                  "the top level must be an object",
		"{\n\"a\":":                            "invalid JSON at line 2",
	} {
		var s settings
		err := json.Unmarshal([]byte(in), &s)
		if err == nil {
			t.Fatalf("%q: expected a decode error", in)
		}
		got := Describe([]byte(in), err)
		if got != want {
			t.Errorf("Describe(%q) = %q, want %q", in, got, want)
		}
		if strings.Contains(got, "Go") || strings.Contains(got, "json:") {
			t.Errorf("Describe(%q) leaks internals: %q", in, got)
		}
	}
}
