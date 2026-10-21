package cli

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

// teamCmd manages the caller's Teams.
func teamCmd(e *env) *cobra.Command {
	c := &cobra.Command{Use: "team", Short: "Teams: add; inside a run: step, done"}
	// Adding approves the Team for the caller and gives them their own
	// copies of its agents; approve is its older name.
	add := &cobra.Command{Use: "add ID", Aliases: []string{"approve"}, Args: cobra.ExactArgs(1), Short: "Add a Team to your studies", RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		if err := e.client().Do(cmd.Context(), "POST", "/api/teams/"+url.PathEscape(a[0])+"/add", nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "added %s\n", a[0])
		return nil
	}}
	c.AddCommand(add, teamStepCmd(e), teamDoneCmd(e))
	return c
}
