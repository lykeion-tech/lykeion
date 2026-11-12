package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"lykeion/server/internal/desktopupdate"
)

func desktopUpdateCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:               "desktop-update-check",
		Hidden:            true,
		Args:              cobra.NoArgs,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := desktopupdate.CheckAndCache(cmd.Context())
			if err != nil {
				return err
			}
			return json.NewEncoder(e.out).Encode(result)
		},
	}
}

func desktopUpdateBuildInfoCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:               "desktop-update-build-info",
		Hidden:            true,
		Args:              cobra.NoArgs,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(_ *cobra.Command, _ []string) error {
			info, err := desktopupdate.EmbeddedBuildTrust()
			if err != nil {
				return err
			}
			return json.NewEncoder(e.out).Encode(info)
		},
	}
}
