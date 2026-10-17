package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var columns = []string{"backlog", "grounding", "hypotheses", "experimentation", "validation", "reporting", "done"}

// steerPost sends one steering action for the card numbered n and prints where it left the card.
func steerPost(cmd *cobra.Command, e *env, n, action string, body any) error {
	if err := e.inWorkspace(); err != nil {
		return err
	}
	c, err := byNumber(cmd, e, n)
	if err != nil {
		return err
	}
	var out cardView
	if err := e.client().Do(cmd.Context(), "POST", "/api/cards/"+c.ID+"/"+action, body, &out); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "#%d %s %s %s\n", out.Number, out.BoardColumn, out.State, out.Flag)
	return nil
}

// boardCmd prints the board, with a Blocked row for cards that need a person.
func boardCmd(e *env) *cobra.Command {
	return &cobra.Command{Use: "board", Short: "The board, with a Blocked row for cards that need a person", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		var cards []struct {
			cardView
			Ending string `json:"ending"`
		}
		if err := e.client().Do(cmd.Context(), "GET", "/api/cards", nil, &cards); err != nil {
			return err
		}
		for _, row := range []string{"Blocked", "Active"} {
			fmt.Fprintf(e.out, "== %s\n", row)
			for _, col := range columns {
				for _, c := range cards {
					blocked := c.Flag == "escalated"
					if c.BoardColumn != col || blocked != (row == "Blocked") {
						continue
					}
					badge := c.State
					switch {
					case c.Ending != "":
						badge = c.Ending
					case blocked:
						badge = c.Flag
					}
					fmt.Fprintf(e.out, "%-16s #%-4d %-14s %s\n", col, c.Number, badge, c.Title)
				}
			}
		}
		return nil
	}}
}

// steerCmds adds stop and resume under `lyk card`.
func steerCmds(e *env, card *cobra.Command) {
	post := func(cmd *cobra.Command, n, action string, body any) error { return steerPost(cmd, e, n, action, body) }
	var reason string
	var dupOf int
	stop := &cobra.Command{Use: "stop N", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		return post(cmd, a[0], "stop", map[string]any{"reason": reason, "duplicate_of": dupOf})
	}}
	stop.Flags().StringVar(&reason, "reason", "", "why")
	stop.Flags().IntVar(&dupOf, "duplicate-of", 0, "the card this one duplicates")
	resume := &cobra.Command{Use: "resume N", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		return post(cmd, a[0], "resume", nil)
	}}
	card.AddCommand(stop, resume)
}
