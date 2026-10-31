package bb

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Comment is the subset of the Bitbucket pull request comment resource that bb uses.
type Comment struct {
	ID      int `json:"id"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
	User struct {
		DisplayName string `json:"display_name"`
	} `json:"user"`
	CreatedOn time.Time `json:"created_on"`
	Deleted   bool      `json:"deleted"`
	Pending   bool      `json:"pending"`
	Parent    *struct {
		ID int `json:"id"`
	} `json:"parent"`
	// Inline is set for comments on a line of the diff; From is the old-file line (removed
	// lines) and To the new-file line (added and unchanged lines).
	Inline *struct {
		Path string `json:"path"`
		From *int   `json:"from"`
		To   *int   `json:"to"`
	} `json:"inline"`
}

// thread is a top-level comment and the replies beneath it.
type thread struct {
	root    Comment
	replies []threadReply
}

type threadReply struct {
	comment Comment
	depth   int // 1 for a direct reply to the root
}

// anchor says which diff line the thread is on, if it is an inline thread.
func (t thread) anchor() (inlineRef, bool) {
	in := t.root.Inline
	if in == nil || in.Path == "" {
		return inlineRef{}, false
	}
	ref := inlineRef{path: in.Path}
	if in.From != nil {
		ref.from = *in.From
	}
	if in.To != nil {
		ref.to = *in.To
	}
	return ref, ref.from > 0 || ref.to > 0
}

func byCreated(a, b Comment) int {
	if c := a.CreatedOn.Compare(b.CreatedOn); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

// buildThreads groups comments into threads, oldest first, with replies in order beneath their
// parent. A deleted comment is only kept when it still has replies to hang under it.
func buildThreads(comments []Comment) []thread {
	known := make(map[int]bool, len(comments))
	for _, c := range comments {
		known[c.ID] = true
	}

	var roots []Comment
	children := map[int][]Comment{}
	for _, c := range comments {
		if c.Parent != nil && c.Parent.ID != c.ID && known[c.Parent.ID] {
			children[c.Parent.ID] = append(children[c.Parent.ID], c)
		} else { // a top-level comment, or a reply whose parent we don't have
			roots = append(roots, c)
		}
	}
	slices.SortStableFunc(roots, byCreated)

	var flatten func(parent, depth int) []threadReply
	flatten = func(parent, depth int) []threadReply {
		kids := children[parent]
		slices.SortStableFunc(kids, byCreated)
		var out []threadReply
		for _, k := range kids {
			out = append(out, threadReply{k, depth})
			out = append(out, flatten(k.ID, depth+1)...)
		}
		return out
	}

	var threads []thread
	for _, r := range roots {
		t := thread{root: r, replies: flatten(r.ID, 1)}
		if r.Deleted && len(t.replies) == 0 {
			continue
		}
		threads = append(threads, t)
	}
	return threads
}

// countComments is the number of live comments across threads.
func countComments(threads []thread) int {
	n := 0
	for _, t := range threads {
		if !t.root.Deleted {
			n++
		}
		for _, r := range t.replies {
			if !r.comment.Deleted {
				n++
			}
		}
	}
	return n
}

// threadsByLine maps each content line of the diff to the indexes (into threads) of the inline
// threads attached to it.
func threadsByLine(lines []diffLine, threads []thread) map[int][]int {
	type pos struct {
		path string
		n    int
	}
	onNew, onOld := map[pos][]int{}, map[pos][]int{}
	for i, t := range threads {
		a, ok := t.anchor()
		switch {
		case !ok:
		case a.to > 0:
			onNew[pos{a.path, a.to}] = append(onNew[pos{a.path, a.to}], i)
		default:
			onOld[pos{a.path, a.from}] = append(onOld[pos{a.path, a.from}], i)
		}
	}

	out := map[int][]int{}
	for i, l := range lines {
		var found []int
		switch l.kind {
		case diffAdd:
			found = onNew[pos{l.path, l.newNo}]
		case diffContext:
			found = append(slices.Clone(onNew[pos{l.path, l.newNo}]), onOld[pos{l.path, l.oldNo}]...)
		case diffDel:
			found = onOld[pos{l.path, l.oldNo}]
		}
		if len(found) > 0 {
			out[i] = found
		}
	}
	return out
}

var commentAuthorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))

// renderThreads lays threads out as text lines no wider than width.
func renderThreads(threads []thread, width int) []string {
	var out []string
	for i, t := range threads {
		if i > 0 {
			out = append(out, "")
		}
		if a, ok := t.anchor(); ok {
			out = append(out, dimStyle.Render(inlineDescription(a)))
		}
		out = append(out, renderComment(t.root, 0, width)...)
		for _, r := range t.replies {
			out = append(out, renderComment(r.comment, r.depth, width)...)
		}
	}
	return out
}

func renderComment(c Comment, depth, width int) []string {
	indent := strings.Repeat("  ", min(depth, 4))
	arrow := ""
	if depth > 0 {
		arrow = "↳ "
	}

	author := cmp.Or(strings.TrimSpace(sanitizeText(c.User.DisplayName)), "unknown")
	meta := "  " + c.CreatedOn.Local().Format("2006-01-02 15:04")
	if c.Pending {
		meta += "  (pending)"
	}
	head := indent + arrow + commentAuthorStyle.Render(author) + dimStyle.Render(meta)
	out := []string{ansi.Truncate(head, max(1, width), "…")}

	bodyIndent := indent + "  "
	body := strings.TrimRight(sanitizeText(c.Content.Raw), " \n")
	if c.Deleted {
		body = "(deleted)"
	}
	wrapAt := max(10, width-ansi.StringWidth(bodyIndent)-1)
	for line := range strings.SplitSeq(body, "\n") {
		for w := range strings.SplitSeq(ansi.Wrap(line, wrapAt, ""), "\n") {
			text := bodyIndent + w
			if c.Deleted {
				text = dimStyle.Render(text)
			}
			out = append(out, text)
		}
	}
	return out
}

// threadView is a scrollable, read-only display of comment threads.
type threadView struct {
	title         string
	threads       []thread
	lines         []string
	offset        int
	width, height int
}

func newThreadView(title string, threads []thread, width, height int) threadView {
	t := threadView{title: title, threads: threads}
	t.resize(width, height)
	return t
}

func (t *threadView) resize(width, height int) {
	t.width, t.height = width, max(1, height)
	t.lines = renderThreads(t.threads, width)
	t.offset = min(t.offset, t.maxOffset())
}

func (t threadView) maxOffset() int { return max(0, len(t.lines)-t.height) }

func (t *threadView) scrollTo(offset int) { t.offset = max(0, min(offset, t.maxOffset())) }

func (t *threadView) handleKey(key string) bool {
	page := max(1, t.height)
	switch key {
	case "j", "down":
		t.scrollTo(t.offset + 1)
	case "k", "up":
		t.scrollTo(t.offset - 1)
	case "pgdown", "space", "f":
		t.scrollTo(t.offset + page)
	case "pgup", "b":
		t.scrollTo(t.offset - page)
	case "d", "ctrl+d":
		t.scrollTo(t.offset + page/2)
	case "u", "ctrl+u":
		t.scrollTo(t.offset - page/2)
	case "g", "home":
		t.scrollTo(0)
	case "G", "end":
		t.scrollTo(t.maxOffset())
	default:
		return false
	}
	return true
}

func (t threadView) percent() int {
	if t.maxOffset() == 0 {
		return 100
	}
	return t.offset * 100 / t.maxOffset()
}

func (t threadView) view() string {
	end := min(len(t.lines), t.offset+t.height)
	out := slices.Clone(t.lines[t.offset:end])
	for len(out) < t.height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func (t threadView) summary() string {
	n := countComments(t.threads)
	return fmt.Sprintf("%d comment%s", n, map[bool]string{true: "", false: "s"}[n == 1])
}
