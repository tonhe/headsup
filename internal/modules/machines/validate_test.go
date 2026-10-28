package machines

import (
	"slices"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	tests := []struct {
		in      string
		ok      bool
		suggest string
	}{
		{"nas-01", true, ""},
		{"a", true, ""},
		{strings.Repeat("a", 63), true, ""},
		{strings.Repeat("a", 64), false, ""},
		{"NAS 01", false, "nas-01"},
		{"-lead", false, "lead"},
		{"tony's_laptop.local", false, "tonys-laptop-local"},
		{"", false, ""},
	}
	for _, tt := range tests {
		err := ValidateName(tt.in)
		if (err == nil) != tt.ok {
			t.Errorf("%q: err = %v, want ok=%v", tt.in, err, tt.ok)
		}
		if tt.suggest != "" && (err == nil || !strings.Contains(err.Error(), `"`+tt.suggest+`"`)) {
			t.Errorf("%q: want suggestion %q in %v", tt.in, tt.suggest, err)
		}
	}
}

func TestParseTags(t *testing.T) {
	tests := []struct {
		in   string
		want []string
		err  bool
	}{
		{"server, tag:prod", []string{"tag:server", "tag:prod"}, false},
		{"Server server", []string{"tag:server"}, false},
		{"", nil, false},
		{"bad_tag", nil, true},
		{"tag:", nil, true},
	}
	for _, tt := range tests {
		got, err := ParseTags(tt.in)
		if (err != nil) != tt.err {
			t.Errorf("%q: err = %v", tt.in, err)
			continue
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%q: got %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestMergeTags(t *testing.T) {
	got := MergeTags([]string{"tag:a", "tag:b"}, []string{"tag:c", "tag:a"}, []string{"tag:b"})
	if want := []string{"tag:a", "tag:c"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
