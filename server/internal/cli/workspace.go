package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

type workspace struct {
	ID, Slug, Name, Role string
}

func workspaceCmd(e *env) *cobra.Command {
	ws := &cobra.Command{Use: "workspace", Short: "Workspaces, members and invitations"}
	ws.AddCommand(
		&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, _ []string) error {
			if err := e.signedIn(); err != nil {
				return err
			}
			var list []workspace
			if err := e.client().Do(cmd.Context(), "GET", "/api/workspaces", nil, &list); err != nil {
				return err
			}
			for _, w := range list {
				mark := " "
				if w.ID == e.cfg.Workspace {
					mark = "*"
				}
				fmt.Fprintf(e.out, "%s %-24s %-8s %s\n", mark, w.Slug, w.Role, w.Name)
			}
			return nil
		}},
		&cobra.Command{Use: "create NAME SLUG", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, a []string) error {
			if err := e.signedIn(); err != nil {
				return err
			}
			var w workspace
			if err := e.client().Do(cmd.Context(), "POST", "/api/workspaces", map[string]string{"name": a[0], "slug": a[1]}, &w); err != nil {
				return err
			}
			fmt.Fprintln(e.out, "Created", w.Slug)
			return nil
		}},
		&cobra.Command{Use: "switch SLUG", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
			if err := e.signedIn(); err != nil {
				return err
			}
			var list []workspace
			if err := e.client().Do(cmd.Context(), "GET", "/api/workspaces", nil, &list); err != nil {
				return err
			}
			for _, w := range list {
				if w.Slug == a[0] {
					e.cfg.Workspace = w.ID
					return SaveConfig(e.path, e.cfg)
				}
			}
			return fmt.Errorf("no workspace %q among yours", a[0])
		}},
		&cobra.Command{Use: "members", RunE: func(cmd *cobra.Command, _ []string) error {
			if err := e.inWorkspace(); err != nil {
				return err
			}
			var ms []struct{ Email, Name, Role string }
			if err := e.client().Do(cmd.Context(), "GET", "/api/workspaces/"+e.cfg.Workspace+"/members", nil, &ms); err != nil {
				return err
			}
			for _, m := range ms {
				fmt.Fprintf(e.out, "%-8s %s %s\n", m.Role, m.Email, m.Name)
			}
			return nil
		}},
		inviteCmd(e),
		&cobra.Command{Use: "accept TOKEN", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
			if err := e.signedIn(); err != nil {
				return err
			}
			var out struct {
				WorkspaceID string `json:"workspace_id"`
			}
			if err := e.client().Do(cmd.Context(), "POST", "/api/invitations/"+a[0]+"/accept", nil, &out); err != nil {
				return err
			}
			e.cfg.Workspace = out.WorkspaceID
			return SaveConfig(e.path, e.cfg)
		}},
	)
	return ws
}

func inviteCmd(e *env) *cobra.Command {
	var role string
	cmd := &cobra.Command{Use: "invite EMAIL", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		if err := e.client().Do(cmd.Context(), "POST", "/api/workspaces/"+e.cfg.Workspace+"/invitations",
			map[string]string{"email": a[0], "role": role}, nil); err != nil {
			return err
		}
		fmt.Fprintln(e.out, "Invitation sent to", a[0])
		return nil
	}}
	cmd.Flags().StringVar(&role, "role", "member", "member or admin")
	return cmd
}
