// Copyright (C) 2019-2021, Lux Partners Limited. All rights reserved.
// See the file LICENSE for licensing terms.

// "spaces-cli" implements spacesvm client operation interface.
package main

import (
	"os"

	"github.com/fatih/color"

	"github.com/skychains/spacesvm/cmd/spaces-cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		color.Red("spaces-cli failed: %v", err)
		os.Exit(1)
	}
	os.Exit(0)
}
