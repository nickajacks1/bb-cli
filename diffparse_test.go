package bb

import (
	"slices"
	"strings"
	"testing"
)

func kinds(lines []diffLine) []diffKind {
	out := make([]diffKind, len(lines))
	for i, l := range lines {
		out[i] = l.kind
	}
	return out
}

func TestParseDiffNumbersAndPaths(t *testing.T) {
	diff := "diff --git a/dir/x.go b/dir/x.go\nindex 1..2 100644\n--- a/dir/x.go\n+++ b/dir/x.go\n" +
		"@@ -10,3 +10,4 @@ func f() {\n keep\n-gone\n+new1\n+new2\n tail\n"
	lines := parseDiff(diff)

	want := []diffLine{
		{kind: diffFile, text: "diff --git a/dir/x.go b/dir/x.go"},
		{kind: diffMeta, text: "index 1..2 100644"},
		{kind: diffMeta, text: "--- a/dir/x.go"},
		{kind: diffMeta, text: "+++ b/dir/x.go"},
		{kind: diffHunk, text: "@@ -10,3 +10,4 @@ func f() {"},
		{kind: diffContext, text: "keep", oldNo: 10, newNo: 10, path: "dir/x.go"},
		{kind: diffDel, text: "gone", oldNo: 11, path: "dir/x.go"},
		{kind: diffAdd, text: "new1", newNo: 11, path: "dir/x.go"},
		{kind: diffAdd, text: "new2", newNo: 12, path: "dir/x.go"},
		{kind: diffContext, text: "tail", oldNo: 12, newNo: 13, path: "dir/x.go"},
	}
	if !slices.Equal(lines, want) {
		t.Errorf("parsed:\n%+v\nwant:\n%+v", lines, want)
	}
}

func TestParseDiffHunkCountsBeatLookalikeHeaders(t *testing.T) {
	// A removed line "-- x" renders as "--- x" and an added "++ y" as "+++ y"; inside a hunk they
	// are content, not file headers.
	diff := "diff --git a/s.sql b/s.sql\n--- a/s.sql\n+++ b/s.sql\n@@ -1,2 +1,2 @@\n--- x\n+++ y\n ctx\n"
	lines := parseDiff(diff)
	if got := kinds(lines[4:]); !slices.Equal(got, []diffKind{diffDel, diffAdd, diffContext}) {
		t.Fatalf("kinds = %v", got)
	}
	if lines[4].text != "-- x" || lines[5].text != "++ y" {
		t.Errorf("texts = %q, %q", lines[4].text, lines[5].text)
	}
	if lines[4].path != "s.sql" || lines[5].path != "s.sql" {
		t.Errorf("paths = %q, %q", lines[4].path, lines[5].path)
	}
}

func TestParseDiffMultipleFilesAndHunks(t *testing.T) {
	diff := "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-o\n+n\n@@ -50,1 +50,2 @@\n c\n+x\n" +
		"diff --git a/b b/b\n--- a/b\n+++ b/b\n@@ -0,0 +1 @@\n+first\n"
	var adds []diffLine
	for _, l := range parseDiff(diff) {
		if l.kind == diffAdd {
			adds = append(adds, l)
		}
	}
	got := []string{}
	for _, l := range adds {
		got = append(got, l.path+":"+l.text)
	}
	if !slices.Equal(got, []string{"a:n", "a:x", "b:first"}) {
		t.Errorf("adds = %v", got)
	}
	if adds[1].newNo != 51 || adds[2].newNo != 1 {
		t.Errorf("numbers = %d, %d; want 51, 1", adds[1].newNo, adds[2].newNo)
	}
}

func TestParseDiffCreatedDeletedRenamedBinary(t *testing.T) {
	diff := "diff --git a/old.txt b/old.txt\ndeleted file mode 100644\n--- a/old.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-bye\n" +
		"diff --git a/n.txt b/n.txt\nnew file mode 100644\n--- /dev/null\n+++ b/n.txt\n@@ -0,0 +1 @@\n+hi\n" +
		"diff --git a/from.txt b/to.txt\nsimilarity index 90%\nrename from from.txt\nrename to to.txt\n--- a/from.txt\n+++ b/to.txt\n@@ -1 +1 @@\n-a\n+b\n" +
		"diff --git a/img.png b/img.png\nBinary files a/img.png and b/img.png differ\n"

	got := map[string]string{}
	for _, l := range parseDiff(diff) {
		if l.isContent() {
			got[l.text] = l.path
		}
	}
	for text, path := range map[string]string{"bye": "old.txt", "hi": "n.txt", "a": "from.txt", "b": "to.txt"} {
		if got[text] != path {
			t.Errorf("line %q has path %q, want %q", text, got[text], path)
		}
	}
}

func TestParseDiffNoNewlineAndEmptyContext(t *testing.T) {
	diff := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n a\n\n-b\n\\ No newline at end of file\n+c\n"
	lines := parseDiff(diff)
	if got := kinds(lines[4:]); !slices.Equal(got, []diffKind{diffContext, diffContext, diffDel, diffMeta, diffAdd}) {
		t.Errorf("kinds = %v", got)
	}
	if lines[5].text != "" || lines[5].oldNo != 2 || lines[7].kind != diffMeta {
		t.Errorf("empty context line parsed as %+v", lines[5])
	}
}

func TestParseDiffTruncatedHunkIsNotAnError(t *testing.T) {
	// A diff cut off mid-hunk (the 4 MiB cap) must still parse what it has.
	lines := parseDiff("diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,50 +1,50 @@\n a\n-b\n+c")
	if len(lines) != 7 || lines[6].kind != diffAdd {
		t.Errorf("lines = %+v", lines)
	}
}

func TestParseDiffSanitizesControlCharacters(t *testing.T) {
	lines := parseDiff("diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -0,0 +1 @@\n+evil\x1b[2J\tx\r\x07\n")
	got := lines[len(lines)-1].text
	if strings.ContainsAny(got, "\x1b\x07\r\t") {
		t.Errorf("control characters survived: %q", got)
	}
	if got != "evil�[2J    x�" {
		t.Errorf("text = %q", got)
	}
}

func TestInlineRef(t *testing.T) {
	tests := []struct {
		line diffLine
		want inlineRef
		ok   bool
	}{
		{diffLine{kind: diffAdd, newNo: 7, path: "f"}, inlineRef{path: "f", to: 7}, true},
		{diffLine{kind: diffContext, oldNo: 6, newNo: 7, path: "f"}, inlineRef{path: "f", to: 7}, true},
		{diffLine{kind: diffDel, oldNo: 5, path: "f"}, inlineRef{path: "f", from: 5}, true},
		{diffLine{kind: diffHunk}, inlineRef{}, false},
		{diffLine{kind: diffAdd, newNo: 1}, inlineRef{}, false}, // no known path
	}
	for _, tc := range tests {
		if got, ok := tc.line.inlineRef(); got != tc.want || ok != tc.ok {
			t.Errorf("%+v: got %+v, %v; want %+v, %v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSplitRowsPairRemovalsWithAdditions(t *testing.T) {
	lines := []diffLine{
		{kind: diffHunk},    // 0
		{kind: diffContext}, // 1
		{kind: diffDel},     // 2
		{kind: diffDel},     // 3
		{kind: diffDel},     // 4
		{kind: diffAdd},     // 5
		{kind: diffAdd},     // 6
		{kind: diffContext}, // 7
		{kind: diffAdd},     // 8
		{kind: diffDel},     // 9
		{kind: diffMeta},    // 10
	}
	want := []diffRow{
		{0, -1},
		{1, 1},
		{2, 5},
		{3, 6},
		{4, -1}, // three removals vs two additions
		{7, 7},
		{-1, 8},
		{9, -1}, // an addition followed by a removal starts a new block
		{10, -1},
	}
	if got := splitRows(lines); !slices.Equal(got, want) {
		t.Errorf("rows = %v\nwant   %v", got, want)
	}
	if got := unifiedRows(lines); len(got) != len(lines) || got[3] != (diffRow{3, -1}) {
		t.Errorf("unified rows = %v", got)
	}
}
