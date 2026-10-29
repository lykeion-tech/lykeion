package cli

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type cardView struct {
	ID          string `json:"id"`
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Question    string `json:"question"`
	BoardColumn string `json:"board_column"`
	State       string `json:"state"`
	Predicates  []struct {
		Name string `json:"name"`
		Text string `json:"text"`
	} `json:"predicates"`
}

// values prints a decision's values: the number alone for one key, else
// key=value pairs in key order.
func values(m map[string]float64) string {
	keys := slices.Sorted(maps.Keys(m))
	if len(keys) == 1 {
		return strconv.FormatFloat(m[keys[0]], 'g', -1, 64)
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + strconv.FormatFloat(m[k], 'g', -1, 64)
	}
	return strings.Join(parts, ", ")
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

// agentID resolves an agent id or the exact name of a live agent to its id.
func agentID(cmd *cobra.Command, e *env, ref string) (string, error) {
	if _, err := uuid.Parse(ref); err == nil {
		return ref, nil
	}
	var agents []struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		ArchivedAt *string `json:"archived_at"`
	}
	if err := e.client().Do(cmd.Context(), "GET", "/api/agents", nil, &agents); err != nil {
		return "", err
	}
	for _, a := range agents {
		if a.Name == ref && a.ArchivedAt == nil {
			return a.ID, nil
		}
	}
	return "", fmt.Errorf("no agent named %q", ref)
}

func cardCmd(e *env) *cobra.Command {
	c := &cobra.Command{Use: "card", Short: "Research cards"}
	var title, followsFrom, machine, agent string
	var variables, predicates []string
	var uploads cardUploadFlags
	newCmd := &cobra.Command{Use: "new QUESTION", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		var out cardView
		in := map[string]any{"question": strings.Join(a, " ")}
		if cmd.Flags().Changed("machine") {
			in["machine"] = machine
		}
		if len(variables) > 0 {
			env := make(map[string]string, len(variables))
			for _, pair := range variables {
				name, value, ok := strings.Cut(pair, "=")
				if !ok {
					return fmt.Errorf("--env requires NAME=VALUE")
				}
				env[name] = value
			}
			in["env"] = env
		}
		if len(predicates) > 0 {
			list := make([]map[string]string, 0, len(predicates))
			for _, pair := range predicates {
				name, text, ok := strings.Cut(pair, "=")
				if !ok {
					return fmt.Errorf("--predicate requires NAME=TEXT")
				}
				list = append(list, map[string]string{"name": name, "text": text})
			}
			in["predicates"] = list
		}
		if title != "" {
			in["title"] = title
		}
		if agent != "" {
			id, err := agentID(cmd, e, agent)
			if err != nil {
				return err
			}
			in["assignee"] = map[string]any{"type": "agent", "id": id}
		}
		if followsFrom != "" {
			previous, err := byNumber(cmd, e, followsFrom)
			if err != nil {
				return err
			}
			in["follows_from"] = []string{previous.ID}
		}
		if err := uploads.upload(cmd.Context(), e.client(), in); err != nil {
			return err
		}
		if err := e.client().Do(cmd.Context(), "POST", "/api/cards", in, &out); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "#%d %s\n", out.Number, out.Title)
		return nil
	}}
	newCmd.Flags().StringArrayVar(&uploads.attachments, "attach", nil, "file to attach (repeatable)")
	newCmd.Flags().StringArrayVar(&uploads.services, "service", nil, "hidden service NAME=DIR (repeatable)")
	newCmd.Flags().StringArrayVar(&uploads.starts, "service-start", nil, "hidden service NAME=COMMAND (repeatable)")
	newCmd.Flags().StringArrayVar(&uploads.clients, "service-client", nil, "public client NAME=RELPATH (repeatable)")
	newCmd.Flags().StringArrayVar(&uploads.variables, "service-env", nil, "service study variable NAME=VAR (repeatable)")
	newCmd.Flags().StringVar(&machine, "machine", "", "machine name or daemon id (default: mine)")
	newCmd.Flags().StringVar(&title, "title", "", "short title (default: from the question)")
	newCmd.Flags().StringVar(&agent, "agent", "", "agent NAME|ID that works the whole study (default: Teams)")
	newCmd.Flags().StringVar(&followsFrom, "follows-from", "", "card number this follows")
	newCmd.Flags().StringArrayVar(&variables, "env", nil, "plain study setting NAME=VALUE (repeatable)")
	newCmd.Flags().StringArrayVar(&predicates, "predicate", nil, "pre-registered result NAME=TEXT, such as 'effect=effect > 0.2' (repeatable)")
	c.AddCommand(newCmd,
		&cobra.Command{Use: "machine CARD NAME|ID|mine", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			if err := e.inWorkspace(); err != nil {
				return err
			}
			card, err := byNumber(cmd, e, args[0])
			if err != nil {
				return err
			}
			var machine any = args[1]
			if args[1] == "mine" {
				machine = nil
			}
			return e.client().Do(cmd.Context(), "PATCH", "/api/cards/"+card.ID+"/machine", map[string]any{"machine": machine}, nil)
		}},
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
			fmt.Fprintf(e.out, "#%d %s\ncolumn: %s  state: %s\n\n%s\n",
				x.Number, x.Title, x.BoardColumn, x.State, x.Question)
			if len(x.Predicates) > 0 {
				var view struct {
					Preregistration []struct {
						Name    string             `json:"name"`
						Outcome string             `json:"outcome"`
						Run     string             `json:"run"`
						Values  map[string]float64 `json:"values"`
					} `json:"preregistration"`
				}
				if err := e.client().Do(cmd.Context(), "GET", "/api/cards/"+x.ID, nil, &view); err != nil {
					return err
				}
				for _, p := range x.Predicates {
					outcome := "not decided yet"
					for _, d := range view.Preregistration {
						if d.Name == p.Name && (d.Outcome == "held" || d.Outcome == "missed") {
							outcome = fmt.Sprintf("%s (%s, run %s)", d.Outcome, values(d.Values), d.Run)
						}
					}
					fmt.Fprintf(e.out, "pre-registered: %s: %s — %s\n", p.Name, p.Text, outcome)
				}
			}
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
	return &cobra.Command{Use: "inbox", Short: "Escalations and releases", RunE: func(cmd *cobra.Command, _ []string) error {
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
