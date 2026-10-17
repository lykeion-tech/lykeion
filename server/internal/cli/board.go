package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"lykeion/server/internal/skills"
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

func boardCmd(e *env) *cobra.Command {
	board := boardView(e)
	var action, reason, evidence string
	var capN int32
	unblock := &cobra.Command{Use: "unblock N", Short: "Take a Blocked study out: raise_cap, add_data, resume or human_run_arrived", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			return steerPost(cmd, e, a[0], "unblock", map[string]any{"action": action, "cap": capN})
		}}
	unblock.Flags().StringVar(&action, "action", "", "raise_cap, add_data, resume or human_run_arrived")
	unblock.Flags().Int32Var(&capN, "cap", 0, "the new cap, for raise_cap")
	closeC := &cobra.Command{Use: "close N", Short: "Stop a Blocked study", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		return steerPost(cmd, e, a[0], "close", map[string]string{"reason": reason})
	}}
	closeC.Flags().StringVar(&reason, "reason", "", "why")
	reopen := &cobra.Command{Use: "reopen N", Short: "Send a released study back to Grounding on new evidence", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			return steerPost(cmd, e, a[0], "reopen", map[string]string{"reason": reason, "evidence": evidence})
		}}
	reopen.Flags().StringVar(&reason, "reason", "", "why")
	reopen.Flags().StringVar(&evidence, "evidence", "", "the new evidence")
	revert := &cobra.Command{Use: "revert N", Short: "Put a study back on its original starting point", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			return steerPost(cmd, e, a[0], "revert-revision", nil)
		}}
	board.AddCommand(unblock, closeC, reopen, revert)
	return board
}

func boardView(e *env) *cobra.Command {
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
					blocked := c.Flag == "escalated" || c.Flag == "out_of_budget"
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

// steerCmds adds send-back, stop, budget, resume and gates under `lyk card`.
func steerCmds(e *env, card *cobra.Command) {
	post := func(cmd *cobra.Command, n, action string, body any) error { return steerPost(cmd, e, n, action, body) }
	var to, step, reason string
	var usd float64
	var dupOf int
	sendBack := &cobra.Command{Use: "send-back N", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		return post(cmd, a[0], "send-back", map[string]string{"to": to, "step": step, "reason": reason})
	}}
	sendBack.Flags().StringVar(&to, "to", "", "earlier stage")
	sendBack.Flags().StringVar(&step, "step", "", "role to redo (default: all stage roles)")
	sendBack.Flags().StringVar(&reason, "reason", "", "why")
	stop := &cobra.Command{Use: "stop N", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		return post(cmd, a[0], "stop", map[string]any{"reason": reason, "duplicate_of": dupOf})
	}}
	stop.Flags().StringVar(&reason, "reason", "", "why")
	stop.Flags().IntVar(&dupOf, "duplicate-of", 0, "the card this one duplicates")
	budget := &cobra.Command{Use: "budget N", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		return post(cmd, a[0], "budget", map[string]float64{"usd": usd})
	}}
	budget.Flags().Float64Var(&usd, "usd", 0, "new budget")
	resume := &cobra.Command{Use: "resume N", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		return post(cmd, a[0], "resume", nil)
	}}
	gates := &cobra.Command{Use: "gates N", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		c, err := byNumber(cmd, e, a[0])
		if err != nil {
			return err
		}
		var gs []struct {
			Body struct {
				Column  string `json:"column"`
				Round   int    `json:"round"`
				Verdict string `json:"verdict"`
				Reason  string `json:"reason"`
			} `json:"body"`
		}
		if err := e.client().Do(cmd.Context(), "GET", "/api/cards/"+c.ID+"/gates", nil, &gs); err != nil {
			return err
		}
		for _, g := range gs {
			fmt.Fprintf(e.out, "%-14s round %-3d %-15s %s\n", g.Body.Column, g.Body.Round, g.Body.Verdict, g.Body.Reason)
		}
		return nil
	}}
	card.AddCommand(sendBack, stop, budget, resume, gates)
}

func installCmd(e *env) *cobra.Command {
	var project string
	cmd := &cobra.Command{Use: "install claude|codex", Args: cobra.ExactArgs(1), Short: "Install the /ground skill into a project", RunE: func(_ *cobra.Command, a []string) error {
		sub, ok := map[string]string{"claude": ".claude/skills", "codex": ".agents/skills"}[a[0]]
		if !ok {
			return fmt.Errorf("unknown harness %q", a[0])
		}
		b, err := skills.FS.ReadFile("lyk/SKILL.md")
		if err != nil {
			return err
		}
		dst := filepath.Join(project, sub, "lyk")
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), b, 0o644); err != nil {
			return err
		}
		fmt.Fprintln(e.out, "Installed", strings.TrimPrefix(filepath.Join(dst, "SKILL.md"), "./"))
		return nil
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project directory")
	return cmd
}
