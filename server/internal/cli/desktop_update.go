package cli

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"lykeion/server/internal/desktopupdate"
)

func desktopUpdateStreamCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use: "desktop-update-stream", Hidden: true, Args: cobra.NoArgs,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, inOK := e.in.(*os.File)
			output, outOK := e.out.(*os.File)
			if !inOK || !outOK {
				return errors.New("update stream unavailable")
			}
			if err := desktopupdate.StreamVerified(cmd.Context(), input, output); err != nil {
				return errors.New("update stream unavailable")
			}
			return nil
		},
	}
}

func desktopUpdateCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:               "desktop-update-check",
		Hidden:            true,
		Args:              cobra.NoArgs,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := desktopupdate.CheckAndCache(cmd.Context())
			if err != nil {
				return json.NewEncoder(e.out).Encode(struct {
					State    string                 `json:"state"`
					Category desktopupdate.Category `json:"category"`
				}{State: "error", Category: desktopupdate.ErrorCategory(err)})
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
