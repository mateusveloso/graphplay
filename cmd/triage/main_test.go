package main

import (
	"flag"
	"slices"
	"testing"
)

func TestParseInterspersed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       []string
		wantPos    []string
		wantThread string
	}{
		{"flag before", []string{"-thread", "t1", "o/r#1"}, []string{"o/r#1"}, "t1"},
		{"flag after", []string{"o/r#1", "-thread", "t1"}, []string{"o/r#1"}, "t1"},
		{"no flag", []string{"o/r#1"}, []string{"o/r#1"}, ""},
		{"two positionals", []string{"a", "-thread", "t1", "b"}, []string{"a", "b"}, "t1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			thread := fs.String("thread", "", "")
			got, err := parseInterspersed(fs, tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.wantPos) || *thread != tt.wantThread {
				t.Fatalf("positional=%v thread=%q", got, *thread)
			}
		})
	}
}
