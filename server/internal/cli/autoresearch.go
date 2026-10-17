package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type apTrigger struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Cron      string     `json:"cron"`
	Tz        string     `json:"tz"`
	Provider  string     `json:"provider"`
	Enabled   bool       `json:"enabled"`
	Signed    bool       `json:"signed"`
	NextRunAt *time.Time `json:"next_run_at"`
}

type apView struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Description   string      `json:"description"`
	ExecutionMode string      `json:"execution_mode"`
	Status        string      `json:"status"`
	PauseReason   string      `json:"pause_reason"`
	Triggers      []apTrigger `json:"triggers"`
}

type apRun struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason"`
	Source     string    `json:"source"`
	PlannedAt  time.Time `json:"planned_at"`
	CardID     *string   `json:"card_id"`
	AgentRunID *string   `json:"agent_run_id"`
	Report     string    `json:"report"`
}

// autoresearchID resolves a full id or a unique prefix of at least 4 characters.
func autoresearchID(cmd *cobra.Command, e *env, arg string) (string, error) {
	var list []apView
	if err := e.client().Do(cmd.Context(), "GET", "/api/autoresearch", nil, &list); err != nil {
		return "", err
	}
	var hits []string
	for _, a := range list {
		if a.ID == arg {
			return a.ID, nil
		}
		if len(arg) >= 4 && strings.HasPrefix(a.ID, arg) {
			hits = append(hits, a.ID)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", fmt.Errorf("no autoresearch %q", arg)
	default:
		return "", fmt.Errorf("%q matches %d autoresearches; give more of the id", arg, len(hits))
	}
}

func printRun(e *env, r apRun) {
	link := ""
	switch {
	case r.CardID != nil:
		link = "card " + *r.CardID
	case r.AgentRunID != nil:
		link = "agent run " + *r.AgentRunID
	}
	fmt.Fprintf(e.out, "%s  %-9s %-16s %-8s %s %s\n", r.PlannedAt.Format(time.RFC3339), r.Status, r.Reason, r.Source, link, r.ID)
}

// parseFilter reads EVENT or EVENT:ACTION,ACTION.
func parseFilter(s string) map[string]any {
	event, actions, ok := strings.Cut(s, ":")
	f := map[string]any{"event": event}
	if ok {
		f["actions"] = strings.Split(actions, ",")
	}
	return f
}

func autoresearchCmd(e *env) *cobra.Command {
	c := &cobra.Command{Use: "autoresearch", Short: "Saved rules that open cards or run work on a schedule, a webhook or on demand"}
	// with runs fn after resolving the autoresearch named by the first argument.
	with := func(fn func(cmd *cobra.Command, id string, a []string) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, a []string) error {
			if err := e.inWorkspace(); err != nil {
				return err
			}
			id, err := autoresearchID(cmd, e, a[0])
			if err != nil {
				return err
			}
			return fn(cmd, id, a[1:])
		}
	}

	var mode, description, template, agent, from, to string
	var teams []string
	create := &cobra.Command{Use: "create TITLE", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		if mode == "" {
			return errors.New("--mode create_card or --mode run_only is required")
		}
		var out apView
		in := map[string]any{"title": strings.Join(a, " "), "execution_mode": mode, "description": description,
			"title_template": template}
		assignee := map[string]any{"type": "agent", "id": agent}
		if len(teams) > 0 {
			byColumn := map[string]string{}
			for _, t := range teams {
				col, id, ok := strings.Cut(t, "=")
				if !ok {
					return errors.New("--team takes COLUMN=TEAM_ID")
				}
				byColumn[col] = id
			}
			assignee = map[string]any{"type": "team", "teams": byColumn}
			if agent != "" {
				assignee["agent_id"] = agent
			}
		} else if agent == "" {
			return errors.New("--agent or --team is required")
		}
		in["assignee"] = assignee
		if from != "" || to != "" {
			in["scope"] = map[string]string{"from": from, "to": to}
		}
		if err := e.client().Do(cmd.Context(), "POST", "/api/autoresearch", in, &out); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "%s %s %s\n", out.ID, out.ExecutionMode, out.Title)
		return nil
	}}
	create.Flags().StringVar(&mode, "mode", "", "create_card (open a Backlog card) or run_only (run one agent that reports)")
	create.Flags().StringVar(&description, "description", "", "what the Autoresearch asks for; a create_card card's question")
	create.Flags().StringVar(&agent, "agent", "", "the assigned agent's id; with --team, the agent for the columns no Team covers")
	create.Flags().StringArrayVar(&teams, "team", nil, "COLUMN=TEAM_ID; repeat for a Team set")
	create.Flags().StringVar(&from, "from", "", "first column of the studies it opens")
	create.Flags().StringVar(&to, "to", "", "last column of the studies it opens")
	create.Flags().StringVar(&template, "template", "", "card title template; {{date}} is the run's day")

	list := &cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		var out []apView
		if err := e.client().Do(cmd.Context(), "GET", "/api/autoresearch", nil, &out); err != nil {
			return err
		}
		for _, a := range out {
			fmt.Fprintf(e.out, "%s  %-7s %-12s %s\n", a.ID[:8], a.Status, a.ExecutionMode, a.Title)
		}
		return nil
	}}

	show := &cobra.Command{Use: "show ID", Args: cobra.ExactArgs(1), RunE: with(func(cmd *cobra.Command, id string, _ []string) error {
		var a apView
		if err := e.client().Do(cmd.Context(), "GET", "/api/autoresearch/"+id, nil, &a); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "%s\n%s  %s %s %s\n", a.Title, a.ID, a.ExecutionMode, a.Status, a.PauseReason)
		if a.Description != "" {
			fmt.Fprintln(e.out, a.Description)
		}
		for _, t := range a.Triggers {
			when := ""
			if t.NextRunAt != nil {
				when = "next " + t.NextRunAt.Format(time.RFC3339)
			}
			fmt.Fprintf(e.out, "- %s %s %s %s %s enabled=%v signed=%v %s\n", t.ID[:min(8, len(t.ID))], t.Kind, t.Cron, t.Tz, t.Provider, t.Enabled, t.Signed, when)
		}
		return nil
	})}

	post := func(cmd *cobra.Command, path string, in, out any) error {
		return e.client().Do(cmd.Context(), "POST", path, in, out)
	}
	var reason string
	pause := &cobra.Command{Use: "pause ID", Args: cobra.ExactArgs(1), RunE: with(func(cmd *cobra.Command, id string, _ []string) error {
		return post(cmd, "/api/autoresearch/"+id+"/pause", map[string]string{"reason": reason}, nil)
	})}
	pause.Flags().StringVar(&reason, "reason", "", "why")
	resume := &cobra.Command{Use: "resume ID", Args: cobra.ExactArgs(1), RunE: with(func(cmd *cobra.Command, id string, _ []string) error {
		return post(cmd, "/api/autoresearch/"+id+"/resume", nil, nil)
	})}

	var cronExpr, tz string
	schedule := &cobra.Command{Use: "schedule ID", Args: cobra.ExactArgs(1), Short: "Fire on a cron schedule in a time zone (default: yours)",
		RunE: with(func(cmd *cobra.Command, id string, _ []string) error {
			var out struct {
				Trigger apTrigger `json:"trigger"`
			}
			if err := post(cmd, "/api/autoresearch/"+id+"/triggers", map[string]string{"kind": "schedule", "cron": cronExpr, "tz": tz}, &out); err != nil {
				return err
			}
			fmt.Fprintf(e.out, "schedule %s added\n", out.Trigger.ID)
			return nil
		})}
	schedule.Flags().StringVar(&cronExpr, "cron", "", `five-field cron, or @daily, @weekly, …`)
	schedule.Flags().StringVar(&tz, "tz", "", "IANA time zone, such as Europe/Copenhagen")

	var provider string
	var secretStdin bool
	var filters []string
	webhook := &cobra.Command{Use: "webhook ID", Args: cobra.ExactArgs(1), Short: "Fire when a webhook is posted",
		RunE: with(func(cmd *cobra.Command, id string, _ []string) error {
			in := map[string]any{"kind": "webhook", "provider": provider}
			if secretStdin {
				s, err := stdinText(e)
				if err != nil {
					return err
				}
				in["signing_secret"] = strings.TrimSpace(s)
			}
			fs := []map[string]any{}
			for _, f := range filters {
				fs = append(fs, parseFilter(f))
			}
			in["filters"] = fs
			var out struct {
				Trigger apTrigger `json:"trigger"`
				URL     string    `json:"url"`
			}
			if err := post(cmd, "/api/autoresearch/"+id+"/triggers", in, &out); err != nil {
				return err
			}
			fmt.Fprintf(e.out, "webhook %s added. Its URL is shown only now; keep it secret:\n%s%s\n",
				out.Trigger.ID, strings.TrimRight(e.cfg.Server, "/"), out.URL)
			return nil
		})}
	webhook.Flags().StringVar(&provider, "provider", "generic", "generic (the URL token is the credential) or github")
	webhook.Flags().BoolVar(&secretStdin, "secret-stdin", false, "read a github signing secret from stdin")
	webhook.Flags().StringArrayVar(&filters, "filter", nil, "EVENT or EVENT:ACTION,ACTION; repeat for more")

	run := &cobra.Command{Use: "run ID", Args: cobra.ExactArgs(1), Short: "Run now, as you", RunE: with(func(cmd *cobra.Command, id string, _ []string) error {
		var r apRun
		if err := post(cmd, "/api/autoresearch/"+id+"/run", nil, &r); err != nil {
			return err
		}
		printRun(e, r)
		return nil
	})}
	runs := &cobra.Command{Use: "runs ID", Args: cobra.ExactArgs(1), RunE: with(func(cmd *cobra.Command, id string, _ []string) error {
		var rs []apRun
		if err := e.client().Do(cmd.Context(), "GET", "/api/autoresearch/"+id+"/runs", nil, &rs); err != nil {
			return err
		}
		for _, r := range rs {
			printRun(e, r)
		}
		return nil
	})}

	subscription := func(method string) func(*cobra.Command, string, []string) error {
		return func(cmd *cobra.Command, id string, _ []string) error {
			var me struct {
				ID string `json:"id"`
			}
			if err := e.client().Do(cmd.Context(), "GET", "/api/me", nil, &me); err != nil {
				return err
			}
			return e.client().Do(cmd.Context(), method, "/api/autoresearch/"+id+"/subscribers/"+me.ID, nil, nil)
		}
	}
	subscribe := &cobra.Command{Use: "subscribe ID", Args: cobra.ExactArgs(1), Short: "Get its new cards and reports in your Inbox",
		RunE: with(subscription("PUT"))}
	unsubscribe := &cobra.Command{Use: "unsubscribe ID", Args: cobra.ExactArgs(1), RunE: with(subscription("DELETE"))}

	c.AddCommand(create, list, show, pause, resume, schedule, webhook, run, runs, subscribe, unsubscribe)
	return c
}
