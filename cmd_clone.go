package main

import (
	"context"

	"github.com/urfave/cli/v3"
)

func cloneCommand() *cli.Command {
	return &cli.Command{
		Name:   "clone",
		Usage:  "Clone a repository",
		Action: runClone,
	}
}

func runClone(ctx context.Context, cmd *cli.Command) error {
	// TODO: implement
	return nil
}
