package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
)

// completeRepoNames returns a shell-completion handler that offers repository names from the
// local cache. It never touches the network, so completion stays instant (and offers nothing
// until `bb ls` has populated the cache). Names already given on the command line are not
// offered again, and flags are still completed after a leading "-"; max caps the number of names accepted (0 means unlimited).
func completeRepoNames(max int) cli.ShellCompleteFunc {
	return func(ctx context.Context, cmd *cli.Command) {
		given := cmd.Args().Slice()
		// Setting ShellComplete replaces urfave's default handling, so restore flag completion.
		if n := len(given); n > 0 && strings.HasPrefix(given[n-1], "-") {
			cli.DefaultCompleteWithFlags(ctx, cmd)
			return
		}
		if max > 0 && len(given) >= max {
			return
		}

		cfg, err := loadConfig()
		if err != nil || cfg.Workspace == "" {
			return
		}
		cache := loadRepoCache(cfg.Workspace)
		if cache == nil {
			return
		}

		for _, r := range cache.Repos {
			if !slices.Contains(given, r.Slug) {
				fmt.Fprintln(cmd.Root().Writer, r.Slug)
			}
		}
	}
}
