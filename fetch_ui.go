package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

type progressMsg struct{ fetched, total int }
type fetchDoneMsg struct{}

// fetchModel renders a spinner, a progress bar, and an optional tip while repositories load.
type fetchModel struct {
	spinner        spinner.Model
	bar            progress.Model
	fetched, total int
	tip            string
	done           bool
}

func newFetchModel(tip string) fetchModel {
	return fetchModel{
		spinner: spinner.New(
			spinner.WithSpinner(spinner.Dot),
			spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("39"))),
		),
		bar: progress.New(progress.WithDefaultBlend(), progress.WithWidth(32)),
		tip: tip,
	}
}

func (m fetchModel) Init() tea.Cmd { return m.spinner.Tick }

func (m fetchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case progressMsg:
		m.fetched, m.total = msg.fetched, msg.total
	case fetchDoneMsg:
		m.done = true
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m fetchModel) View() tea.View {
	if m.done {
		return tea.NewView("") // leave no UI behind
	}

	line := m.spinner.View() + " Fetching repositories"
	if m.total > 0 {
		pct := float64(m.fetched) / float64(m.total)
		line += fmt.Sprintf("  %s  %d/%d", m.bar.ViewAs(min(pct, 1)), m.fetched, m.total)
	} else if m.fetched > 0 {
		line += fmt.Sprintf("  %d so far", m.fetched)
	}
	if m.tip != "" {
		line += "\n" + dimStyle.Render("Tip: "+m.tip)
	}
	return tea.NewView(line + "\n")
}

// fetchRepositories loads all repositories, showing progress on stderr when it is a terminal.
// tip, if non-empty, is displayed while loading.
func fetchRepositories(ctx context.Context, cfg *Config, tip string) ([]Repository, error) {
	if !term.IsTerminal(os.Stderr.Fd()) {
		return listRepositories(ctx, cfg, nil)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// No input: Ctrl-C stays a plain SIGINT, which bubbletea reports as ErrInterrupted.
	p := tea.NewProgram(newFetchModel(tip), tea.WithOutput(os.Stderr), tea.WithInput(nil))

	var (
		repos    []Repository
		fetchErr error
		finished = make(chan struct{})
	)
	go func() {
		defer close(finished)
		repos, fetchErr = listRepositories(ctx, cfg, func(fetched, total int) {
			p.Send(progressMsg{fetched, total})
		})
		p.Send(fetchDoneMsg{})
	}()

	_, runErr := p.Run()
	cancel()
	<-finished

	if errors.Is(runErr, tea.ErrInterrupted) {
		return nil, errors.New("interrupted")
	}
	if fetchErr != nil {
		return nil, fetchErr
	}
	return repos, runErr
}
