package bb

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

// completeConfigNames offers config option names for the first argument of `configure get/set`.
func completeConfigNames(ctx context.Context, cmd *cli.Command) {
	given := cmd.Args().Slice()
	if n := len(given); n > 0 && strings.HasPrefix(given[n-1], "-") {
		cli.DefaultCompleteWithFlags(ctx, cmd)
		return
	}
	if len(given) > 0 {
		return
	}
	for _, name := range configOptionNames() {
		fmt.Fprintln(cmd.Root().Writer, name)
	}
}

// completionValue is a value offered for a flag, with a short description some shells display.
type completionValue struct{ value, description string }

// completeFlagValues returns a shell-completion handler that offers values for the string flag
// name (given as "--name VALUE" or "--name=VALUE") and otherwise defers to next.
//
// The shell scripts pass only the words before the cursor, plus the current word if it starts
// with "-". So after "--name " the flag has no value yet and is left at the end of the
// arguments, while "--name=par" arrives complete with the partial value in the flag. A value
// that is already complete can't be told apart from "--name VALUE " (completing what follows),
// so that case is left to next.
func completeFlagValues(name string, values []completionValue, next cli.ShellCompleteFunc) cli.ShellCompleteFunc {
	return func(ctx context.Context, cmd *cli.Command) {
		out := cmd.Root().Writer
		offer := func(prefix, partial string) {
			for _, v := range values {
				if strings.HasPrefix(v.value, partial) {
					fmt.Fprintf(out, "%s%s:%s\n", prefix, v.value, v.description)
				}
			}
		}

		given := cmd.Args().Slice()
		if n := len(given); n > 0 && (given[n-1] == "--"+name || given[n-1] == "-"+name) {
			offer("", "")
			return
		}
		if partial := cmd.String(name); cmd.IsSet(name) && !slices.ContainsFunc(values, func(v completionValue) bool { return v.value == partial }) &&
			slices.ContainsFunc(values, func(v completionValue) bool { return strings.HasPrefix(v.value, partial) }) {
			offer("--"+name+"=", partial)
			return
		}
		next(ctx, cmd)
	}
}
