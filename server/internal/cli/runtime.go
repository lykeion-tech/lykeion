package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func runtimeCmd(e *env) *cobra.Command {
	r := &cobra.Command{Use: "runtime", Short: "Machines that run agents in this workspace"}
	r.AddCommand(&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := e.inWorkspace(); err != nil {
			return err
		}
		var runtimes []struct {
			ID         string `json:"id"`
			Harness    string `json:"harness"`
			DeviceName string `json:"device_name"`
			Status     string `json:"status"`
			LastSeenAt string `json:"last_seen_at"`
		}
		if err := e.client().Do(cmd.Context(), "GET", "/api/runtimes", nil, &runtimes); err != nil {
			return err
		}
		for _, runtime := range runtimes {
			fmt.Fprintf(e.out, "%-8s %-8s %-20s %s\n", runtime.Harness, runtime.Status, runtime.DeviceName, runtime.LastSeenAt)
		}
		return nil
	}})
	return r
}
