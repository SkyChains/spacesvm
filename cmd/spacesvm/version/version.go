// Copyright (C) 2019-2021, Lux Partners Limited. All rights reserved.
// See the file LICENSE for licensing terms.

// Package version implements "version" commands.
package version

import (
	"fmt"

	"github.com/skychains/spacesvm/version"
	"github.com/skychains/spacesvm/vm"
	"github.com/spf13/cobra"
)

func init() {
	cobra.EnablePrefixMatching = true
}

// NewCommand implements "spacesvm version" command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Prints out the verson",
		RunE:  versionFunc,
	}
	return cmd
}

func versionFunc(cmd *cobra.Command, args []string) error {
	fmt.Printf("%s@%s\n", vm.Name, version.Version)
	return nil
}
