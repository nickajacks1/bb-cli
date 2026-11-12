package bb

import (
	"cmp"
	"regexp"
	"strconv"
	"strings"
)

type diffKind int

const (
	diffMeta    diffKind = iota // index/mode/---/+++ lines, "\ No newline at end of file", notes
	diffFile                    // "diff --git a/x b/x"
	diffHunk                    // "@@ -1,3 +1,4 @@"
	diffContext                 // unchanged line
	diffAdd                     // added line
	diffDel                     // removed line
)

// diffLine is one parsed line of a unified diff.
type diffLine struct {
	kind diffKind
	// text is the line without its leading +/-/space marker (content lines) or as-is (others),
	// sanitized and with tabs expanded so it is safe and predictable to print.
	text         string
	oldNo, newNo int    // line numbers in the old and new file; 0 where not applicable
	path         string // the file a content line belongs to (the old path for removed lines)
}

// isContent reports whether the line is a line of file content (context, added or removed).
func (l diffLine) isContent() bool {
	return l.kind == diffContext || l.kind == diffAdd || l.kind == diffDel
}

// inlineRef locates a line for an inline pull request comment: a path plus the old line number
// (removed lines) or the new one (added and context lines).
type inlineRef struct {
	path     string
	from, to int
}

func (l diffLine) inlineRef() (inlineRef, bool) {
	if l.path == "" {
		return inlineRef{}, false
	}
	switch l.kind {
	case diffAdd, diffContext:
		return inlineRef{path: l.path, to: l.newNo}, l.newNo > 0
	case diffDel:
		return inlineRef{path: l.path, from: l.oldNo}, l.oldNo > 0
	}
	return inlineRef{}, false
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// sanitizeText makes diff text safe to print: carriage returns are dropped, tabs become spaces
// and other control characters (which could carry terminal escape sequences from an
// untrusted diff) are replaced with U+FFFD.
func sanitizeText(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			return '�'
		}
		return r
	}, s)
}

// parseDiff parses a unified diff into lines annotated with their kind, line numbers and file.
// Hunk bodies are delimited by the line counts in their headers, so a removed line whose text
// starts with "-- " is not mistaken for a file header.
func parseDiff(diff string) []diffLine {
	raw := strings.Split(sanitizeText(diff), "\n")
	if n := len(raw); n > 0 && raw[n-1] == "" {
		raw = raw[:n-1]
	}

	var (
		out              []diffLine
		oldPath, newPath string
		oldNo, newNo     int
		oldLeft, newLeft int // lines still expected in the current hunk
		addContent       = func(kind diffKind, text string) {
			l := diffLine{kind: kind, text: text}
			switch kind {
			case diffContext:
				l.oldNo, l.newNo, l.path = oldNo, newNo, cmp.Or(newPath, oldPath)
				oldNo, newNo, oldLeft, newLeft = oldNo+1, newNo+1, oldLeft-1, newLeft-1
			case diffAdd:
				l.newNo, l.path = newNo, cmp.Or(newPath, oldPath)
				newNo, newLeft = newNo+1, newLeft-1
			case diffDel:
				l.oldNo, l.path = oldNo, cmp.Or(oldPath, newPath)
				oldNo, oldLeft = oldNo+1, oldLeft-1
			}
			out = append(out, l)
		}
	)

	for _, l := range raw {
		if oldLeft > 0 || newLeft > 0 {
			switch {
			case strings.HasPrefix(l, `\`):
				out = append(out, diffLine{kind: diffMeta, text: l})
				continue
			case strings.HasPrefix(l, "+") && newLeft > 0:
				addContent(diffAdd, l[1:])
				continue
			case strings.HasPrefix(l, "-") && oldLeft > 0:
				addContent(diffDel, l[1:])
				continue
			case strings.HasPrefix(l, " ") && oldLeft > 0 && newLeft > 0:
				addContent(diffContext, l[1:])
				continue
			case l == "" && oldLeft > 0 && newLeft > 0: // an empty context line whose space was stripped
				addContent(diffContext, "")
				continue
			}
			oldLeft, newLeft = 0, 0 // malformed or cut-off hunk: read on as headers
		}

		switch {
		case strings.HasPrefix(l, "diff "):
			oldPath, newPath = "", ""
			if rest, ok := strings.CutPrefix(l, "diff --git "); ok {
				if fields := strings.Fields(rest); len(fields) == 2 {
					oldPath = strings.TrimPrefix(fields[0], "a/")
					newPath = strings.TrimPrefix(fields[1], "b/")
				}
			}
			out = append(out, diffLine{kind: diffFile, text: l})
		case strings.HasPrefix(l, "--- "):
			oldPath = diffPath(l[4:], "a/")
			out = append(out, diffLine{kind: diffMeta, text: l})
		case strings.HasPrefix(l, "+++ "):
			newPath = diffPath(l[4:], "b/")
			out = append(out, diffLine{kind: diffMeta, text: l})
		case strings.HasPrefix(l, "rename from "):
			oldPath = strings.TrimPrefix(l, "rename from ")
			out = append(out, diffLine{kind: diffMeta, text: l})
		case strings.HasPrefix(l, "rename to "):
			newPath = strings.TrimPrefix(l, "rename to ")
			out = append(out, diffLine{kind: diffMeta, text: l})
		default:
			if m := hunkHeader.FindStringSubmatch(l); m != nil {
				oldNo, oldLeft = atoi(m[1]), countOrOne(m[2])
				newNo, newLeft = atoi(m[3]), countOrOne(m[4])
				out = append(out, diffLine{kind: diffHunk, text: l})
				continue
			}
			out = append(out, diffLine{kind: diffMeta, text: l})
		}
	}
	return out
}

// diffPath extracts a file path from a ---/+++ line, dropping the a/ or b/ prefix and any
// trailing timestamp. /dev/null (a created or deleted file) yields "".
func diffPath(s, prefix string) string {
	s, _, _ = strings.Cut(s, "\t")
	s = strings.TrimSpace(s)
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(s, prefix)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func countOrOne(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}

// diffRow is one screen row of the diff viewer: the lines shown on its left and right (indexes
// into the parsed lines, -1 for none). Unified rows only use left.
type diffRow struct{ left, right int }

// unifiedRows lays the diff out one line per row.
func unifiedRows(lines []diffLine) []diffRow {
	rows := make([]diffRow, len(lines))
	for i := range lines {
		rows[i] = diffRow{i, -1}
	}
	return rows
}

// splitRows lays the diff out side by side: context lines appear on both sides, and each run of
// removed lines is paired row by row with the run of added lines that follows it. Headers and
// other non-content lines take a row of their own.
func splitRows(lines []diffLine) []diffRow {
	var (
		rows       []diffRow
		dels, adds []int
	)
	flush := func() {
		for k := range max(len(dels), len(adds)) {
			r := diffRow{-1, -1}
			if k < len(dels) {
				r.left = dels[k]
			}
			if k < len(adds) {
				r.right = adds[k]
			}
			rows = append(rows, r)
		}
		dels, adds = nil, nil
	}

	for i, l := range lines {
		switch l.kind {
		case diffDel:
			if len(adds) > 0 { // a new run of removals starts after additions
				flush()
			}
			dels = append(dels, i)
		case diffAdd:
			adds = append(adds, i)
		case diffContext:
			flush()
			rows = append(rows, diffRow{i, i})
		default:
			flush()
			rows = append(rows, diffRow{i, -1})
		}
	}
	flush()
	return rows
}
