package bb

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// A diff with an unequal change block, a pure addition, wide characters and a long line.
var viewerDiff = "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
	"@@ -1,5 +1,6 @@\n" +
	" ctx one\n" +
	"-old a\n" +
	"-old b\n" +
	"-old c\n" +
	"+new a\n" +
	"+日本語 wide\n" +
	" ctx two\n" +
	"+" + strings.Repeat("long ", 60) + "\n" +
	" ctx three\n"

func plain(s string) string { return ansi.Strip(s) }

func TestDiffViewerSplitRowsHaveConsistentWidth(t *testing.T) {
	const width = 120
	v := newDiffViewer(viewerDiff, width, 30, true)
	if !v.rowsSplit {
		t.Fatal("expected the split layout at 120 columns")
	}

	cw := v.cellWidth()
	for i := range v.rows {
		row := v.renderRow(i)
		w := lipgloss.Width(row)
		if w > width {
			t.Errorf("row %d is %d wide, over the %d columns available: %q", i, w, width, plain(row))
		}
		if r := v.rows[i]; (r.left >= 0 && v.lines[r.left].isContent()) || r.right >= 0 {
			if w != 2*cw+1 {
				t.Errorf("content row %d is %d wide, want %d (both sides must line up): %q", i, w, 2*cw+1, plain(row))
			}
			if strings.Count(plain(row), "│") != 1 {
				t.Errorf("row %d should have exactly one divider: %q", i, plain(row))
			}
		}
	}
}

func TestDiffViewerSplitContent(t *testing.T) {
	v := newDiffViewer(viewerDiff, 120, 30, true)
	out := plain(v.view())

	for _, want := range []string{"old a", "new a", "日本語 wide", "ctx one", "…"} {
		if !strings.Contains(out, want) {
			t.Errorf("split view missing %q:\n%s", want, out)
		}
	}
	// The first change row pairs "old a" and "new a" side by side.
	found := false
	for line := range strings.SplitSeq(out, "\n") {
		left, right, ok := strings.Cut(line, "│")
		if ok && strings.Contains(left, "old a") && strings.Contains(right, "new a") {
			found = true
		}
	}
	if !found {
		t.Errorf("old a and new a should share a row:\n%s", out)
	}
	if strings.Contains(out, "+new") || strings.Contains(out, "-old") {
		t.Errorf("split view shouldn't repeat the +/- markers:\n%s", out)
	}
}

func TestDiffViewerUnifiedKeepsMarkersAndPans(t *testing.T) {
	v := newDiffViewer(viewerDiff, 60, 30, false)
	out := plain(v.view())
	for _, want := range []string{"-old a", "+new a", "  ctx one"} {
		if !strings.Contains(out, want) {
			t.Errorf("unified view missing %q:\n%s", want, out)
		}
	}

	if v.xoff != 0 {
		t.Fatal("should start unpanned")
	}
	v.handleKey("right")
	if v.xoff != panStep || strings.Contains(plain(v.view()), "-old a") {
		t.Errorf("panning right should shift the text (xoff %d)", v.xoff)
	}
	for range 100 {
		v.handleKey("right")
	}
	if v.xoff > v.maxPan() || v.xoff == 0 {
		t.Errorf("pan should clamp to the longest line: xoff %d, max %d", v.xoff, v.maxPan())
	}
	for range 100 {
		v.handleKey("left")
	}
	if v.xoff != 0 {
		t.Errorf("pan should clamp at zero, got %d", v.xoff)
	}
}

func TestDiffViewerSplitFallsBackWhenNarrow(t *testing.T) {
	v := newDiffViewer(viewerDiff, 60, 30, true)
	if v.rowsSplit {
		t.Errorf("60 columns is below the %d needed for split", v.minSplitWidth())
	}

	// Moving to a changed line, then widening and narrowing, keeps the cursor on that line.
	v.moveTo(7) // "+new a" in the unified layout
	line := v.cursorLine()
	v.resize(v.minSplitWidth(), 30)
	if !v.rowsSplit || v.cursorLine() != line {
		t.Errorf("widening should switch to split and keep line %d (split %v, line %d)", line, v.rowsSplit, v.cursorLine())
	}
	v.resize(v.minSplitWidth()-1, 30)
	if v.rowsSplit || v.cursorLine() != line {
		t.Errorf("narrowing should fall back to unified and keep line %d (split %v, line %d)", line, v.rowsSplit, v.cursorLine())
	}
}

func TestDiffViewerCursorAndScrolling(t *testing.T) {
	v := newDiffViewer(viewerDiff, 100, 4, false)
	n := len(v.rows)

	for range n + 5 {
		v.handleKey("j")
		if v.cursor < v.offset || v.cursor >= v.offset+4 {
			t.Fatalf("cursor %d left the window starting at %d", v.cursor, v.offset)
		}
	}
	if v.cursor != n-1 {
		t.Errorf("j should stop at the last row (%d), got %d", n-1, v.cursor)
	}

	v.handleKey("g")
	if v.cursor != 0 || v.offset != 0 {
		t.Errorf("g: cursor %d, offset %d", v.cursor, v.offset)
	}
	v.handleKey("G")
	if v.cursor != n-1 {
		t.Errorf("G: cursor %d", v.cursor)
	}
	v.handleKey("pgup")
	if v.cursor != n-1-4 {
		t.Errorf("pgup should move a page: cursor %d", v.cursor)
	}
	if got := strings.Count(v.view(), "\n") + 1; got != 4 {
		t.Errorf("view should always be exactly 4 lines, got %d", got)
	}
	if !strings.Contains(plain(v.view()), "▌") {
		t.Error("the cursor row should be marked")
	}
}

func TestDiffViewerCursorSides(t *testing.T) {
	v := newDiffViewer(viewerDiff, 120, 30, true)
	// Find the first change row: "old a" / "new a".
	for i, r := range v.rows {
		if r.left >= 0 && r.right >= 0 && r.left != r.right {
			v.moveTo(i)
			break
		}
	}

	if line, _, _ := v.commentLine(); line.kind != diffAdd {
		t.Errorf("the new side is active by default, got %v", line.kind)
	}
	v.handleKey("h")
	line, ref, ok := v.commentLine()
	if line.kind != diffDel || !ok || ref.from != 2 || ref.to != 0 || ref.path != "f.go" {
		t.Errorf("old side: %+v %+v %v", line, ref, ok)
	}
	v.handleKey("l")
	if _, ref, _ := v.commentLine(); ref.to != 2 || ref.from != 0 {
		t.Errorf("new side: %+v", ref)
	}

	// Rows with a single side fall back to it whichever side is active.
	for i, r := range v.rows {
		if r.right < 0 && r.left >= 0 && v.lines[r.left].kind == diffDel {
			v.moveTo(i)
			break
		}
	}
	v.handleKey("l")
	if line, _, _ := v.commentLine(); line.kind != diffDel {
		t.Errorf("a removal-only row should fall back to its only line, got %v", line.kind)
	}
}

func TestDiffViewerEmptyDiff(t *testing.T) {
	v := newDiffViewer("", 80, 5, true)
	if !strings.Contains(plain(v.view()), "no changes") {
		t.Errorf("empty diff view = %q", plain(v.view()))
	}
	if _, _, ok := v.commentLine(); ok {
		t.Error("nothing to comment on in an empty diff")
	}
}
