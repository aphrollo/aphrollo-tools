package core

import (
	"testing"
	"time"
)

func TestFormatBytes_RendersTheUnitAnOperatorReads(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		// exp caps at 3 (TB); a size beyond that still renders in TB rather
		// than overflowing to a fourth unit letter.
		{1024 * 1024 * 1024 * 1024, "1.0 TB"},
		{1024 * 1024 * 1024 * 1024 * 1024, "1024.0 TB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.n); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestFormatDays_WholeDaysOrHoursBelowOne(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0h"},
		{23 * time.Hour, "23h"},
		{24 * time.Hour, "1d"},
		{25 * time.Hour, "1d"},
		{48 * time.Hour, "2d"},
	}
	for _, c := range cases {
		if got := formatDays(c.d); got != c.want {
			t.Errorf("formatDays(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
