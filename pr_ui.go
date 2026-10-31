package bb

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	maxPRTitleLen  = 60
	maxPRBranchLen = 30
)

var prHeaders = []string{"#", "TITLE", "AUTHOR", "BRANCH", "UPDATED"}

func prRows(prs []PullRequest) [][]string {
	rows := make([][]string, len(prs))
	for i, p := range prs {
		rows[i] = []string{
			fmt.Sprintf("#%d", p.ID),
			truncate(p.Title, maxPRTitleLen),
			truncate(p.Author.DisplayName, maxPRBranchLen),
			truncate(p.Source.Branch.Name, maxPRBranchLen),
			p.UpdatedOn.Local().Format(time.DateOnly),
		}
	}
	return rows
}

// renderPRTable renders a static table, used when output isn't an interactive terminal.
func renderPRTable(prs []PullRequest) string {
	return renderTable(prHeaders, prRows(prs), 0)
}

// prSelector is an interactive table of pull requests; enter picks one, q/esc/ctrl+c cancels.
type prSelector struct {
	table    table.Model
	prs      []PullRequest
	selected *PullRequest
	width    int
	height   int

	// loadDiff fetches a pull request's diff for the `v` preview; nil disables the preview.
	loadDiff func(PullRequest) (diff string, truncated bool, err error)
	mode     prMode
	diffPR   PullRequest    // the pull request being previewed
	diff     viewport.Model // the scrollable preview
	diffNote string         // shown under the preview, e.g. that the diff was cut short
	notice   string         // one-off error shown under the list
}

func newPRSelector(prs []PullRequest, loadDiff func(PullRequest) (string, bool, error)) prSelector {
	rows := prRows(prs)

	// Size each column to its widest cell so nothing is cut off.
	cols := make([]table.Column, len(prHeaders))
	for i, h := range prHeaders {
		cols[i] = table.Column{Title: h, Width: lipgloss.Width(h)}
	}
	tableRows := make([]table.Row, len(rows))
	for r, row := range rows {
		tableRows[r] = row
		for c, cell := range row {
			cols[c].Width = max(cols[c].Width, lipgloss.Width(cell))
		}
	}
	width := 0 // the viewport clips to its width, so it must cover every padded column
	for _, c := range cols {
		width += c.Width + 2
	}

	styles := table.DefaultStyles()
	styles.Header = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).Padding(0, 1)
	styles.Selected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111")).Reverse(true)

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(tableRows),
		table.WithStyles(styles),
		table.WithFocused(true),
		table.WithWidth(width),
		table.WithHeight(min(len(rows), 15)+1),
	)
	return prSelector{table: t, prs: prs, loadDiff: loadDiff, width: 80, height: 24}
}

func (m prSelector) Init() tea.Cmd { return nil }

func (m prSelector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Leave room for the help line and a blank line; the header row counts toward the height.
		m.table.SetHeight(max(3, min(len(m.prs)+1, msg.Height-3)))
		m.diff.SetWidth(msg.Width)
		m.diff.SetHeight(max(1, msg.Height-diffChrome))
		return m, nil
	case prDiffMsg:
		return m.diffLoaded(msg), nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.mode {
		case modeLoadingDiff:
			if k := msg.String(); k == "q" || k == "esc" {
				m.mode = modeList
			}
			return m, nil
		case modeDiff:
			return m.updateDiff(msg)
		}
		m.notice = ""
		switch msg.String() {
		case "enter":
			m.selected = &m.prs[m.table.Cursor()]
			return m, tea.Quit
		case "q", "esc":
			return m, tea.Quit
		case "v":
			if m.loadDiff == nil {
				return m, nil
			}
			m.mode, m.diffPR = modeLoadingDiff, m.prs[m.table.Cursor()]
			return m, m.fetchDiff(m.diffPR)
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m prSelector) View() tea.View {
	var content string
	switch m.mode {
	case modeLoadingDiff:
		content = dimStyle.Render(fmt.Sprintf("Loading the diff of #%d...  (q to cancel)", m.diffPR.ID))
	case modeDiff:
		content = m.diffView()
	default:
		help := "↑/↓ navigate • enter open in browser • v view diff • q quit"
		if m.loadDiff == nil {
			help = "↑/↓ navigate • enter open in browser • q quit"
		}
		content = m.table.View() + "\n\n"
		if m.notice != "" {
			content += errorStyle.Render(m.notice) + "\n"
		}
		content += dimStyle.Render(help)
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// selectPullRequest shows the interactive table and returns the chosen pull request, or nil if
// the user cancelled. loadDiff, if non-nil, powers the `v` diff preview.
func selectPullRequest(prs []PullRequest, loadDiff func(PullRequest) (string, bool, error)) (*PullRequest, error) {
	final, err := tea.NewProgram(newPRSelector(prs, loadDiff)).Run()
	if err != nil {
		return nil, err
	}
	return final.(prSelector).selected, nil
}
