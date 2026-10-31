package main

import (
	"context"
	"fmt"
	"os"

	bb "github.com/nickajacks1/bb-cli"
)

func main() {
	if err := bb.NewApp().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
