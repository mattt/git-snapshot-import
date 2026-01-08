package snapshot_test

import (
	"testing"
	"time"

	"github.com/mattt/git-snapshot-import/internal/snapshot"
)

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantErr bool
	}{
		{
			name:  "date only",
			input: "1787-09-17",
			want:  time.Date(1787, 9, 17, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "with time",
			input: "1787-09-17T16:00:00",
			want:  time.Date(1787, 9, 17, 16, 0, 0, 0, time.UTC),
		},
		{
			name:  "with UTC marker",
			input: "1787-09-17T16:00:00Z",
			want:  time.Date(1787, 9, 17, 16, 0, 0, 0, time.UTC),
		},
		{
			name:  "with timezone offset",
			input: "1787-09-17T11:00:00-05:00",
			want:  time.Date(1787, 9, 17, 11, 0, 0, 0, time.FixedZone("", -5*3600)),
		},
		{
			name:    "invalid format",
			input:   "not-a-date",
			wantErr: true,
		},
		{
			name:    "partial date",
			input:   "1787-09",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := snapshot.ParseTimestamp(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTimestamp() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Equal(tt.want) {
				t.Errorf("ParseTimestamp() = %v, want %v", got, tt.want)
			}
		})
	}
}

