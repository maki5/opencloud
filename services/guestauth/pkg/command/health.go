package command

import (
	"github.com/opencloud-eu/opencloud/services/guestauth/pkg/config"
	"github.com/spf13/cobra"
)

// Health is the entrypoint for the health command.
func Health(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Check health status",
		RunE: func(cmd *cobra.Command, args []string) error {
			// not implemented
			return nil
		},
	}
}
