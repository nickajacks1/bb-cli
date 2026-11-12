package bb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/urfave/cli/v3"
)

// maxParallelLookups caps the repository requests made at once.
const maxParallelLookups = 8

func openCommand() *cli.Command {
	return &cli.Command{
		Name:      "open",
		Usage:     "Open repositories in the browser",
		ArgsUsage: "REPO...",
		Description: "Opens each repository's page in your browser, and prints the URLs.\n" +
			"Every name is checked first, so a typo opens nothing.",
		ShellComplete: completeRepoNames(0),
		Action:        runOpen,
	}
}

func runOpen(ctx context.Context, cmd *cli.Command) error {
	slugs := dedupe(cmd.Args().Slice())
	if len(slugs) == 0 {
		return errors.New("usage: bb open <repo>...")
	}

	cfg, err := requireConfig()
	if err != nil {
		return err
	}

	urls, err := repoWebURLs(ctx, cfg, slugs)
	if err != nil {
		return err
	}
	return openURLs(cmd.Root().Writer, urls, launchBrowser)
}

// repoWebURLs looks up each repository and returns its web URL, in the order given. If any
// don't exist it returns an error naming all of them, so the caller can open none.
func repoWebURLs(ctx context.Context, cfg *Config, slugs []string) ([]string, error) {
	urls := make([]string, len(slugs))
	errs := make([]error, len(slugs))

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallelLookups)
	for i, slug := range slugs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			repo, err := getRepository(ctx, cfg, slug)
			if err != nil {
				errs[i] = err
				return
			}
			urls[i] = repo.webURL(cfg.Workspace)
		})
	}
	wg.Wait()

	var missing []string
	for _, err := range errs {
		var nf *repoNotFoundError
		switch {
		case err == nil:
		case errors.As(err, &nf):
			missing = append(missing, nf.slug)
		default:
			return nil, err // auth, network...: the same for every repository, so say it once
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("no such repositories in workspace %q (nothing was opened): %s", cfg.Workspace, strings.Join(missing, ", "))
	}
	return urls, nil
}

// openURLs prints each URL to out and opens it with open. It tries all of them even if some
// fail to open, then reports how many did.
func openURLs(out io.Writer, urls []string, open func(string) error) error {
	var failed []error
	for _, u := range urls {
		fmt.Fprintln(out, u)
		if err := open(u); err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	lipgloss.Fprintln(os.Stderr, dimStyle.Render("The URLs are printed above."))
	return fmt.Errorf("couldn't open %d of %d in a browser: %w", len(failed), len(urls), failed[0])
}

// dedupe returns ss without repeats, keeping the first of each and the original order.
func dedupe(ss []string) []string {
	var out []string
	for _, s := range ss {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
