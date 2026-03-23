package main

import "testing"

func TestParseLimit(t *testing.T) {
	cases := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"", 10, false},
		{"5", 5, false},
		{"100", 100, false},
		{"1", 1, false},
		{"0", 0, true},
		{"-1", 0, true},
		{"101", 0, true},
		{"abc", 0, true},
		{"  ", 10, false},
	}
	for _, tc := range cases {
		got, err := parseLimit(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseLimit(%q): wantErr=%v, got err=%v", tc.input, tc.wantErr, err)
		}
		if err == nil && got != tc.want {
			t.Errorf("parseLimit(%q): want %d, got %d", tc.input, tc.want, got)
		}
	}
}

func TestParsePage(t *testing.T) {
	cases := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"", 1, false},
		{"1", 1, false},
		{"5", 5, false},
		{"100", 100, false},
		{"0", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
		{"  ", 1, false},
	}
	for _, tc := range cases {
		got, err := parsePage(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("parsePage(%q): wantErr=%v, got err=%v", tc.input, tc.wantErr, err)
		}
		if err == nil && got != tc.want {
			t.Errorf("parsePage(%q): want %d, got %d", tc.input, tc.want, got)
		}
	}
}

// TODO(Phase 2B): add integration tests for PostgresSearchRepository using
// testcontainers-go. Tests must cover: Search, ListFlags, GetFlag,
// GetFlagHistory, GetTimeline, org_id scoping (org A cannot see org B data),
// pagination, and 404 for unknown flag key.
