package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type cardView struct {
	ID          string  `json:"id"`
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	Question    string  `json:"question"`
	BoardColumn string  `json:"board_column"`
	State       string  `json:"state"`
	Flag        string  `json:"flag"`
	BudgetUsd   float64 `json:"budget_usd"`
	SpentUsd    float64 `json:"spent_usd"`
}

// byNumber resolves a card number to its id.
func byNumber(cmd *cobra.Command, e *env, arg string) (cardView, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(arg, "#"))
	if err != nil {
		return cardView{}, fmt.Errorf("card number expected, got %q", arg)
	}
	var cards []cardView
	if err := e.client().Do(cmd.Context(), "GET", "/api/cards", nil, &cards); err != nil {
		return cardView{}, err
	}
	for _, c := range cards {
		if c.Number == n {
			return c, nil
		}
	}
	return cardView{}, fmt.Errorf("no card #%d", n)
}

func cardCmd(e *env) *cobra.Command {
	c := &cobra.Command{Use: "card", Short: "Research cards"}
	var title, followsFrom string
	var usd float64
	newCmd := &cobra.Command{Use: "new QUESTION", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		var out cardView
		in := map[string]any{"question": strings.Join(a, " "), "budget_usd": usd}
		if title != "" {
			in["title"] = title
		}
		if followsFrom != "" {
			previous, err := byNumber(cmd, e, followsFrom)
			if err != nil {
				return err
			}
			in["follows_from"] = []string{previous.ID}
		}
		if err := e.client().Do(cmd.Context(), "POST", "/api/cards", in, &out); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "#%d %s\n", out.Number, out.Title)
		return nil
	}}
	newCmd.Flags().StringVar(&title, "title", "", "short title (default: from the question)")
	newCmd.Flags().Float64Var(&usd, "usd", 0, "model budget in USD")
	newCmd.Flags().StringVar(&followsFrom, "follows-from", "", "card number this follows")
	c.AddCommand(newCmd,
		&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, _ []string) error {
			if err := e.inWorkspace(); err != nil {
				return err
			}
			var cards []cardView
			if err := e.client().Do(cmd.Context(), "GET", "/api/cards", nil, &cards); err != nil {
				return err
			}
			for _, x := range cards {
				fmt.Fprintf(e.out, "#%-4d %-16s %-10s %s\n", x.Number, x.BoardColumn, x.State, x.Title)
			}
			return nil
		}},
		&cobra.Command{Use: "show NUMBER", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
			if err := e.inWorkspace(); err != nil {
				return err
			}
			x, err := byNumber(cmd, e, a[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(e.out, "#%d %s\ncolumn: %s  state: %s  flag: %s\nbudget: $%.2f spent: $%.2f\n\n%s\n",
				x.Number, x.Title, x.BoardColumn, x.State, x.Flag, x.BudgetUsd, x.SpentUsd, x.Question)
			var comments []struct {
				Body      string `json:"body"`
				CreatedAt string `json:"created_at"`
			}
			if err := e.client().Do(cmd.Context(), "GET", "/api/cards/"+x.ID+"/comments", nil, &comments); err != nil {
				return err
			}
			for _, cm := range comments {
				fmt.Fprintf(e.out, "\n— %s\n%s\n", cm.CreatedAt, cm.Body)
			}
			return nil
		}},
		&cobra.Command{Use: "comment NUMBER TEXT", Args: cobra.MinimumNArgs(2), RunE: func(cmd *cobra.Command, a []string) error {
			if err := e.inWorkspace(); err != nil {
				return err
			}
			x, err := byNumber(cmd, e, a[0])
			if err != nil {
				return err
			}
			return e.client().Do(cmd.Context(), "POST", "/api/cards/"+x.ID+"/comments", map[string]string{"body": strings.Join(a[1:], " ")}, nil)
		}},
	)
	steerCmds(e, c)
	return c
}

func inboxCmd(e *env) *cobra.Command {
	return &cobra.Command{Use: "inbox", Short: "Escalations, budgets and releases", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		var items []struct {
			Kind, Text string
			CreatedAt  string  `json:"created_at"`
			ReadAt     *string `json:"read_at"`
		}
		if err := e.client().Do(cmd.Context(), "GET", "/api/inbox", nil, &items); err != nil {
			return err
		}
		for _, it := range items {
			mark := "•"
			if it.ReadAt != nil {
				mark = " "
			}
			fmt.Fprintf(e.out, "%s %-14s %s  %s\n", mark, it.Kind, it.CreatedAt, it.Text)
		}
		return nil
	}}
}
