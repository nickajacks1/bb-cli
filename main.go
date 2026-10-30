package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

func newApp() *cli.Command {
	return &cli.Command{
		Name:                  "bb",
		Usage:                 "A command line interface for Bitbucket",
		EnableShellCompletion: true,
		ConfigureShellCompletionCommand: func(c *cli.Command) {
			c.Hidden = false // urfave hides it by default
		},
		Commands: []*cli.Command{
			listCommand(),
			configureCommand(),
			cloneCommand(),
		},
	}
}

func main() {
	cmd := newApp()
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
