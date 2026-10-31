package bb

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	panStep      = 8  // columns moved by ←/→ in the unified layout
	minSplitText = 40 // each side of the split layout needs at least this much room for code
)

var (
	diffAddStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	diffDelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	diffHunkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("37"))
	diffFileStyle = lipgloss.NewStyle().Bold(true)
	diffMarkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
)

// diffViewer is a scrollable diff with a line cursor, shown either as a unified diff or as two
// side-by-side columns. It only renders the rows on screen, so large diffs stay cheap.
type diffViewer struct {
	lines []diffLine
	rows  []diffRow

	rowsSplit bool // whether rows currently holds the split layout
	wantSplit bool // the layout the user asked for; split falls back to unified when too narrow

	cursor int // index into rows
	offset int // first visible row
	xoff   int // horizontal pan, unified layout only
	side   int // split layout: 0 = old (left), 1 = new (right); which side comments attach to

	width, height int
	numWidth      int // digits in the widest line number
	maxText       int // widest line of text, for panning
}

func newDiffViewer(diff string, width, height int, split bool) diffViewer {
	lines := parseDiff(diff)
	if len(lines) == 0 {
		lines = []diffLine{{kind: diffMeta, text: "(this pull request has no changes)"}}
	}

	v := diffViewer{lines: lines, wantSplit: split, side: 1, width: width, height: max(1, height)}
	most := 0
	for _, l := range lines {
		most = max(most, l.oldNo, l.newNo)
		v.maxText = max(v.maxText, ansi.StringWidth(l.text)+1) // +1 for the +/-/space marker
	}
	v.numWidth = len(strconv.Itoa(most))
	v.layout()
	return v
}

func (v *diffViewer) resize(width, height int) {
	v.width, v.height = width, max(1, height)
	v.layout()
}

// minSplitWidth is the narrowest terminal that can show the split layout.
func (v diffViewer) minSplitWidth() int { return 2*(v.numWidth+2+minSplitText) + 1 }

// layout rebuilds the rows if the effective layout changed (a toggle, or a resize across the
// split threshold), keeping the cursor on the same diff line, and re-clamps scrolling.
func (v *diffViewer) layout() {
	split := v.wantSplit && v.width >= v.minSplitWidth()
	if v.rows == nil || split != v.rowsSplit {
		anchor := v.cursorLine()
		if split {
			v.rows = splitRows(v.lines)
		} else {
			v.rows = unifiedRows(v.lines)
		}
		v.rowsSplit = split

		v.cursor = 0
		for i, r := range v.rows {
			if anchor >= 0 && (r.left == anchor || r.right == anchor) {
				v.cursor = i
				break
			}
		}
	}
	v.xoff = min(max(v.xoff, 0), v.maxPan())
	v.moveTo(v.cursor)
}

// cursorLine is the index of the diff line under the cursor, or -1. In the split layout the
// active side wins when it has a line in the row.
func (v diffViewer) cursorLine() int {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return -1
	}
	r := v.rows[v.cursor]
	first, second := r.left, r.right
	if v.rowsSplit && v.side == 1 {
		first, second = r.right, r.left
	}
	if first >= 0 {
		return first
	}
	return second
}

// commentLine returns the line under the cursor and where an inline comment on it would go.
func (v diffViewer) commentLine() (diffLine, inlineRef, bool) {
	i := v.cursorLine()
	if i < 0 {
		return diffLine{}, inlineRef{}, false
	}
	ref, ok := v.lines[i].inlineRef()
	return v.lines[i], ref, ok
}

func (v *diffViewer) moveTo(row int) {
	v.cursor = max(0, min(row, len(v.rows)-1))
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if v.cursor >= v.offset+v.height {
		v.offset = v.cursor - v.height + 1
	}
	v.offset = max(0, v.offset)
}

func (v *diffViewer) move(n int) { v.moveTo(v.cursor + n) }

func (v diffViewer) percent() int {
	if len(v.rows) <= 1 {
		return 100
	}
	return v.cursor * 100 / (len(v.rows) - 1)
}

// handleKey applies a navigation key and reports whether it was one.
func (v *diffViewer) handleKey(key string) bool {
	page := max(1, v.height)
	switch key {
	case "j", "down":
		v.move(1)
	case "k", "up":
		v.move(-1)
	case "pgdown", "space", "f":
		v.move(page)
	case "pgup", "b":
		v.move(-page)
	case "d", "ctrl+d":
		v.move(page / 2)
	case "u", "ctrl+u":
		v.move(-page / 2)
	case "g", "home":
		v.moveTo(0)
	case "G", "end":
		v.moveTo(len(v.rows) - 1)
	case "left", "h":
		v.horizontal(-1)
	case "right", "l":
		v.horizontal(1)
	default:
		return false
	}
	return true
}

// horizontal pans the unified layout, or picks the old/new side of the split layout.
func (v *diffViewer) horizontal(dir int) {
	if v.rowsSplit {
		v.side = max(0, dir) // left = old side, right = new side
		return
	}
	v.xoff = min(max(v.xoff+dir*panStep, 0), v.maxPan())
}

func (v diffViewer) maxPan() int {
	if v.rowsSplit {
		return 0
	}
	return max(0, v.maxText-v.textWidth())
}

// textWidth is the room for code in the unified layout, after the line-number gutter.
func (v diffViewer) textWidth() int { return max(1, v.width-(2*v.numWidth+3)) }

// cellWidth is the width of one side of the split layout, line number gutter included.
func (v diffViewer) cellWidth() int { return (v.width - 1) / 2 }

func (v diffViewer) view() string {
	out := make([]string, 0, v.height)
	for i := v.offset; i < min(len(v.rows), v.offset+v.height); i++ {
		out = append(out, v.renderRow(i))
	}
	for len(out) < v.height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func (v diffViewer) renderRow(i int) string {
	r := v.rows[i]
	isCursor := i == v.cursor
	if !v.rowsSplit {
		return v.renderUnified(v.lines[r.left], isCursor)
	}

	if r.left >= 0 && r.right < 0 && !v.lines[r.left].isContent() {
		l := v.lines[r.left]
		return mark(isCursor) + kindStyle(l.kind).Render(ansi.Truncate(l.text, max(1, v.width-1), "…"))
	}

	cell := func(idx, num int, here bool) string {
		cw := v.cellWidth()
		if idx < 0 {
			return strings.Repeat(" ", cw)
		}
		l := v.lines[idx]
		tw := max(1, cw-(v.numWidth+2))
		text := ansi.Truncate(l.text, tw, "…")
		text += strings.Repeat(" ", max(0, tw-ansi.StringWidth(text)))
		return mark(here) + dimStyle.Render(v.num(num)) + " " + kindStyle(l.kind).Render(text)
	}

	rightActive := (v.side == 1 && r.right >= 0) || r.left < 0
	var oldNo, newNo int
	if r.left >= 0 {
		oldNo = v.lines[r.left].oldNo
	}
	if r.right >= 0 {
		newNo = v.lines[r.right].newNo
	}
	return cell(r.left, oldNo, isCursor && !rightActive) + dimStyle.Render("│") + cell(r.right, newNo, isCursor && rightActive)
}

func (v diffViewer) renderUnified(l diffLine, isCursor bool) string {
	text := l.text
	switch l.kind {
	case diffAdd:
		text = "+" + text
	case diffDel:
		text = "-" + text
	case diffContext:
		text = " " + text
	}
	text = ansi.Cut(text, v.xoff, v.xoff+v.textWidth())
	return mark(isCursor) + dimStyle.Render(v.num(l.oldNo)+" "+v.num(l.newNo)) + " " + kindStyle(l.kind).Render(text)
}

// num right-aligns a line number in the gutter; zero (no line) is blank.
func (v diffViewer) num(n int) string {
	if n == 0 {
		return strings.Repeat(" ", v.numWidth)
	}
	return fmt.Sprintf("%*d", v.numWidth, n)
}

func mark(here bool) string {
	if here {
		return diffMarkStyle.Render("▌")
	}
	return " "
}

func kindStyle(k diffKind) lipgloss.Style {
	switch k {
	case diffAdd:
		return diffAddStyle
	case diffDel:
		return diffDelStyle
	case diffHunk:
		return diffHunkStyle
	case diffFile:
		return diffFileStyle
	case diffMeta:
		return dimStyle
	}
	return lipgloss.NewStyle()
}
