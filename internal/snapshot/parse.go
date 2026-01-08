package snapshot

import (
	"fmt"
	"regexp"
	"time"
)

// Pre-compiled timestamp patterns (most specific to least specific)
var timestampPatterns = []struct {
	re     *regexp.Regexp
	layout string
}{
	{regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2})$`), "2006-01-02T15:04:05-07:00"},
	{regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z)$`), "2006-01-02T15:04:05Z"},
	{regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})$`), "2006-01-02T15:04:05"},
	{regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})$`), "2006-01-02"},
}

// ParseTimestamp parses ISO 8601 timestamp formats from directory names
func ParseTimestamp(name string) (time.Time, error) {
	for i, p := range timestampPatterns {
		if matches := p.re.FindStringSubmatch(name); matches != nil {
			t, err := time.Parse(p.layout, matches[1])
			if err != nil {
				continue
			}
			// For date-only format (last pattern), ensure UTC
			if i == len(timestampPatterns)-1 {
				t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("not a valid timestamp format")
}
