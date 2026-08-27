// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"fmt"

	"github.com/alibaba/open-code-review/internal/delegation"
	"github.com/spf13/cobra"
)

func newSessionCommand() *cobra.Command {
	session := &cobra.Command{Use: "session", Short: "Inspect delegated review sessions"}
	var repo string
	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List prepared sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := reviewRepository(repo)
			if err != nil {
				return err
			}
			sessions, err := delegation.ListSessions(root)
			if err != nil {
				return err
			}
			if asJSON {
				return writeCommandJSON(cmd, sessions)
			}
			if len(sessions) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No delegated review sessions found.")
				return nil
			}
			for _, item := range sessions {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%d unit(s)\t%s\n", item.SessionID, item.Mode, item.Units, item.State)
			}
			return nil
		},
	}
	list.Flags().StringVar(&repo, "repo", "", "repository containing sessions")
	list.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	session.AddCommand(list)
	return session
}
