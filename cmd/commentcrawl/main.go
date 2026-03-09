package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "commentcrawl",
		Short: "Find sites with WordPress or Disqus comments via Common Crawl",
	}

	root.AddCommand(discoverCmd())
	root.AddCommand(discoverDisqusCmd())
	root.AddCommand(verifyCmd())
	root.AddCommand(runCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
