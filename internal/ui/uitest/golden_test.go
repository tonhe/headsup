package uitest

import (
	"testing"

	"github.com/muesli/termenv"
)

func TestSGRBeyond(t *testing.T) {
	cases := []struct {
		in         string
		ansi, none bool // flagged for 16 colours, flagged for NO_COLOR
	}{
		{"plain", false, false},
		{"\x1b[0mreset\x1b[m", false, false},
		{"\x1b[1;7mbold reverse\x1b[0m", false, true},
		{"\x1b[91;102mbright\x1b[0m", false, true},
		{"\x1b[38;5;203mindexed\x1b[0m", true, true},
		{"\x1b[1;48;2;0;255;0mtrue\x1b[0m", true, true},
	}
	for _, c := range cases {
		if got := len(sgrBeyond(c.in, termenv.ANSI)) > 0; got != c.ansi {
			t.Errorf("%q ANSI: flagged=%v", c.in, got)
		}
		if got := len(sgrBeyond(c.in, termenv.Ascii)) > 0; got != c.none {
			t.Errorf("%q Ascii: flagged=%v", c.in, got)
		}
	}
}
