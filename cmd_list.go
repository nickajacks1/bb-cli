package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/urfave/cli/v3"
)

const maxDescriptionLen = 60

const (
	sortName    = "name"
	sortUpdated = "updated"
)

func listCommand() *cli.Command {
	return &cli.Command{
		Name:    "list",
		Aliases: []string{"ls"},
		Usage:   "List repositories in the workspace",
		Description: "Results are cached for 14 days. Large listings are shown in a pager\n" +
			"($PAGER, default less) when printing to a terminal.",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "stale",
				Usage: "use the cached list even if it is out of date",
			},
			&cli.BoolFlag{
				Name:  "refresh",
				Usage: "ignore the cache and fetch a fresh list",
			},
			&cli.BoolFlag{
				Name:    "quiet",
				Aliases: []string{"q"},
				Usage:   "print only repository names, one per line",
			},
			&cli.StringFlag{
				Name:  "sort",
				Value: sortName,
				Usage: "sort by `COLUMN`: name (A-Z) or updated (newest first)",
			},
		},
		Action: runList,
	}
}

func runList(ctx context.Context, cmd *cli.Command) error {
	stale, refresh := cmd.Bool("stale"), cmd.Bool("refresh")
	if stale && refresh {
		return errors.New("--stale and --refresh can't be used together")
	}

	sortBy := cmd.String("sort")
	if sortBy != sortName && sortBy != sortUpdated {
		return fmt.Errorf("invalid --sort %q: must be %q or %q", sortBy, sortName, sortUpdated)
	}

	cfg, err := requireConfig()
	if err != nil {
		return err
	}

	repos, cachedAt, err := loadRepositories(ctx, cfg, stale, refresh)
	if err != nil {
		return err
	}
	quiet := cmd.Bool("quiet")
	if len(repos) == 0 {
		if !quiet {
			fmt.Printf("No repositories found in workspace %q\n", cfg.Workspace)
		}
		return nil
	}

	sortRepositories(repos, sortBy)
	if quiet {
		names := make([]string, len(repos))
		for i, r := range repos {
			names[i] = r.Slug
		}
		printPaged(strings.Join(names, "\n"), len(names))
		return nil // keep stdout and stderr clean for scripting
	}
	printPaged(renderRepoTable(repos), len(repos)+1)

	if !cachedAt.IsZero() {
		lipgloss.Fprintln(os.Stderr, dimStyle.Render(fmt.Sprintf(
			"Cached results from %s. Run `bb ls --refresh` to update.", cachedAt.Local().Format("2006-01-02"))))
	}
	return nil
}

// loadRepositories returns the workspace's repositories from the cache when it is fresh
// (or when stale is set), otherwise from Bitbucket. cachedAt is non-zero only when the
// result came from the cache.
func loadRepositories(ctx context.Context, cfg *Config, stale, refresh bool) (repos []Repository, cachedAt time.Time, err error) {
	cache := loadRepoCache(cfg.Workspace)
	if cache != nil && !refresh && (stale || !cache.expired(time.Now())) {
		return cache.Repos, cache.FetchedAt, nil
	}

	// The --stale tip is only truthful when there is a cache to fall back on.
	var tip string
	if cache != nil {
		tip = "skip the refresh by running `bb ls --stale`"
	}

	repos, err = fetchRepositories(ctx, cfg, tip)
	if err != nil {
		return nil, time.Time{}, err
	}

	if err := saveRepoCache(cfg.Workspace, repos); err != nil {
		lipgloss.Fprintln(os.Stderr, errorStyle.Render("warning: couldn't cache results: "+err.Error()))
	}
	return repos, time.Time{}, nil
}

// sortRepositories sorts repos in place by name (A-Z) or last update (newest first).
func sortRepositories(repos []Repository, by string) {
	slices.SortStableFunc(repos, func(a, b Repository) int {
		if by == sortUpdated {
			if c := b.UpdatedOn.Compare(a.UpdatedOn); c != 0 {
				return c
			}
		}
		return strings.Compare(a.Slug, b.Slug)
	})
}

func renderRepoTable(repos []Repository) string {
	rows := make([][]string, 0, len(repos))
	for _, r := range repos {
		rows = append(rows, []string{
			r.Slug,
			r.UpdatedOn.Local().Format("2006-01-02"),
			truncate(r.Description, maxDescriptionLen),
		})
	}

	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).PaddingRight(2)
	cell := lipgloss.NewStyle().PaddingRight(2)
	name := cell.Foreground(lipgloss.Color("111"))
	return table.New().
		Border(lipgloss.HiddenBorder()).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).BorderHeader(false).
		Headers("NAME", "UPDATED", "DESCRIPTION").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return header
			}
			if col == 0 {
				return name
			}
			return cell
		}).
		Render()
}

func truncate(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ") // collapse newlines so rows stay on one line
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
