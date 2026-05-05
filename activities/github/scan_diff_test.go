package github

import (
	"testing"
)

func TestScanDiffForFlags(t *testing.T) {
	flags := []string{"my-flag", "other-flag"}

	tests := []struct {
		name        string
		rawDiff     string
		wantAdded   []string
		wantRemoved []string
	}{
		{
			name: "flag on added line only",
			rawDiff: makeDiff("foo.go", `@@ -1,2 +1,3 @@
 context
+  enabled := flagClient.BoolVariation("my-flag", false)
`),
			wantAdded: []string{"my-flag"},
		},
		{
			name: "flag on removed line only",
			rawDiff: makeDiff("foo.go", `@@ -1,3 +1,2 @@
 context
-  enabled := flagClient.BoolVariation("my-flag", false)
`),
			wantRemoved: []string{"my-flag"},
		},
		{
			name: "flag on both lines classified as added (modified)",
			rawDiff: makeDiff("foo.go", `@@ -1,3 +1,3 @@
 context
-  old := flagClient.BoolVariation("my-flag", false)
+  new := flagClient.BoolVariation("my-flag", true)
`),
			wantAdded: []string{"my-flag"},
		},
		{
			name: "multiple flags",
			rawDiff: makeDiff("foo.go", `@@ -1,3 +1,3 @@
 context
+  a := flags.Get("my-flag")
-  b := flags.Get("other-flag")
`),
			wantAdded:   []string{"my-flag"},
			wantRemoved: []string{"other-flag"},
		},
		{
			name: "no flags in diff",
			rawDiff: makeDiff("foo.go", `@@ -1,2 +1,2 @@
 context
+  unrelated line
`),
		},
		{
			name: "dotfile skipped",
			rawDiff: makeDiff(".env", `@@ -1,2 +1,2 @@
 context
+  my-flag = true
`),
		},
		{
			name: "pure rename skipped",
			rawDiff: makeRenameDiff("old.go", "new.go", `@@ -1,2 +1,2 @@
 context
+  my-flag = true
`),
		},
		{
			name:    "empty flag keys returns empty result",
			rawDiff: makeDiff("foo.go", "@@ -1 +1 @@\n+my-flag\n"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keys := flags
			if tc.name == "empty flag keys returns empty result" {
				keys = nil
			}

			result, err := ScanDiffForFlags(tc.rawDiff, keys)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			assertStringSliceEqual(t, "Added", tc.wantAdded, result.Added)
			assertStringSliceEqual(t, "Removed", tc.wantRemoved, result.Removed)
		})
	}
}

func makeDiff(filename, hunk string) string {
	return "diff --git a/" + filename + " b/" + filename + "\n" +
		"--- a/" + filename + "\n" +
		"+++ b/" + filename + "\n" +
		hunk
}

func makeRenameDiff(from, to, hunk string) string {
	return "diff --git a/" + from + " b/" + to + "\n" +
		"--- a/" + from + "\n" +
		"+++ b/" + to + "\n" +
		hunk
}

func assertStringSliceEqual(t *testing.T, label string, want, got []string) {
	t.Helper()
	wantSet := toSet(want)
	gotSet := toSet(got)
	for k := range wantSet {
		if _, ok := gotSet[k]; !ok {
			t.Errorf("%s: expected %q but not found (got %v)", label, k, got)
		}
	}
	for k := range gotSet {
		if _, ok := wantSet[k]; !ok {
			t.Errorf("%s: unexpected %q (want %v)", label, k, want)
		}
	}
}

func toSet(s []string) map[string]struct{} {
	m := make(map[string]struct{}, len(s))
	for _, v := range s {
		m[v] = struct{}{}
	}
	return m
}
