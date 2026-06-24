package main

import "testing"

func TestParseQuitOutput(t *testing.T) {
	cases := []struct {
		out  string
		want quitDecision
	}{
		{"button returned:Quit & Stop watching\n", quitStop},
		{"button returned:Quit & Keep watching\n", quitKeep},
		{"", quitCancel},
		{"button returned:Something else\n", quitCancel},
	}
	for _, c := range cases {
		if got := parseQuitOutput(c.out); got != c.want {
			t.Errorf("parseQuitOutput(%q) = %d, want %d", c.out, got, c.want)
		}
	}
}
