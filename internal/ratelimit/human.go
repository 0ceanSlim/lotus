package ratelimit

import (
	"fmt"
	"strconv"
	"time"
)

// HumanBytes formats a byte count the way the frontends do: binary units,
// labelled KB/MB/GB, at most one decimal.
func HumanBytes(n int64) string {
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	format := func(v float64, unit string) string {
		s := strconv.FormatFloat(v, 'f', 1, 64)
		if len(s) > 2 && s[len(s)-2:] == ".0" {
			s = s[:len(s)-2]
		}
		return s + " " + unit
	}
	switch {
	case n >= gb:
		return format(float64(n)/gb, "GB")
	case n >= mb:
		return format(float64(n)/mb, "MB")
	case n >= kb:
		return format(float64(n)/kb, "KB")
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// HumanDuration renders a window for people: "day", "12 hours", "90 minutes".
func HumanDuration(d time.Duration) string {
	plural := func(n int64, unit string) string {
		if n == 1 {
			return unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	switch {
	case d > 0 && d%(24*time.Hour) == 0:
		return plural(int64(d/(24*time.Hour)), "day")
	case d > 0 && d%time.Hour == 0:
		return plural(int64(d/time.Hour), "hour")
	case d > 0 && d%time.Minute == 0:
		return plural(int64(d/time.Minute), "minute")
	default:
		return d.String()
	}
}
