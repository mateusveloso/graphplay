package main

import (
	"flag"
	"slices"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		wantPos  []string
		wantStop bool
	}{
		{"flag before", []string{"-stop", "t1"}, []string{"t1"}, true},
		{"flag after", []string{"t1", "-stop"}, []string{"t1"}, true},
		{"no flag", []string{"t1"}, []string{"t1"}, false},
		{"none", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			stop := fs.Bool("stop", false, "")
			got, err := parseInterspersed(fs, tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.wantPos) || *stop != tt.wantStop {
				t.Fatalf("positional=%v stop=%v", got, *stop)
			}
		})
	}
}
