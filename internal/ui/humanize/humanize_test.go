package humanize

import (
	"testing"
	"time"
)

func TestAgoIn(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		got, want string
	}{
		{Ago(time.Time{}, now), "never"},
		{Ago(now.Add(-2*time.Second), now), "now"},
		{Ago(now.Add(-42*time.Second), now), "42s ago"},
		{Ago(now.Add(-5*time.Minute), now), "5m ago"},
		{Ago(now.Add(-3*time.Hour), now), "3h ago"},
		{Ago(now.Add(-72*time.Hour), now), "3d ago"},
		{Ago(now.Add(-30*24*time.Hour), now), "4w ago"},
		{In(now.Add(90*time.Minute), now), "in 1h"},
		{In(now.Add(-time.Hour), now), "1h ago"},
		{Ago(now.Add(time.Hour), now), "in 1h"},
	}
	for i, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%d: got %q, want %q", i, tt.got, tt.want)
		}
	}
}
