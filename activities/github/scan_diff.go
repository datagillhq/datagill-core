package github

import (
	"fmt"
	"strings"

	"github.com/sourcegraph/go-diff/diff"
)

// ScanResult holds the flag keys found in a PR diff, split by added and removed.
type ScanResult struct {
	Added   []string // flag keys found on "+" lines (introduced or modified)
	Removed []string // flag keys found only on "-" lines
}

// ScanDiffForFlags parses a raw unified diff and returns which flag keys appear
// on added vs removed lines. Logic mirrors launchdarkly/find-code-references-in-pull-request.
//
// Classification rules:
//   - A flag key on a "+" line → Added
//   - A flag key only on "-" lines → Removed
//   - A flag key on both "+" and "-" lines → Added (modified)
func ScanDiffForFlags(rawDiff string, flagKeys []string) (ScanResult, error) {
	if len(flagKeys) == 0 {
		return ScanResult{}, nil
	}

	files, err := diff.ParseMultiFileDiff([]byte(rawDiff))
	if err != nil {
		return ScanResult{}, fmt.Errorf("parse diff: %w", err)
	}

	added := make(map[string]struct{})
	removed := make(map[string]struct{})

	for _, file := range files {
		if skipFile(file) {
			continue
		}
		for _, hunk := range file.Hunks {
			scanHunk(hunk.Body, flagKeys, added, removed)
		}
	}

	result := ScanResult{}
	for key := range added {
		result.Added = append(result.Added, key)
	}
	for key := range removed {
		if _, isAdded := added[key]; !isAdded {
			result.Removed = append(result.Removed, key)
		}
	}
	return result, nil
}

// skipFile returns true for dotfiles and pure renames (no content change),
// matching the filtering logic in launchdarkly/find-code-references-in-pull-request.
func skipFile(file *diff.FileDiff) bool {
	origName := stripDiffPrefix(file.OrigName)
	newName := stripDiffPrefix(file.NewName)

	// Skip dotfiles (both sides must be dotfiles).
	if strings.HasPrefix(origName, ".") && strings.HasPrefix(newName, ".") {
		return true
	}

	// Skip pure renames (a != b and neither side is /dev/null).
	if origName != newName &&
		!strings.Contains(newName, "dev/null") &&
		!strings.Contains(origName, "dev/null") {
		return true
	}

	return false
}

// stripDiffPrefix removes the leading "a/" or "b/" git diff prefix from a filename.
func stripDiffPrefix(name string) string {
	if _, after, ok := strings.Cut(name, "/"); ok {
		return after
	}
	return name
}

func scanHunk(body []byte, flagKeys []string, added, removed map[string]struct{}) {
	for _, line := range strings.Split(string(body), "\n") {
		if len(line) == 0 {
			continue
		}

		var isAdd, isDel bool
		switch line[0] {
		case '+':
			isAdd = true
		case '-':
			isDel = true
		default:
			continue // context line
		}

		content := line[1:]
		for _, key := range flagKeys {
			if strings.Contains(content, key) {
				if isAdd {
					added[key] = struct{}{}
				} else if isDel {
					removed[key] = struct{}{}
				}
			}
		}
	}
}
