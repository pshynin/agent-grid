package cli

import (
	"github.com/spf13/cobra"
)

const version = "0.0.0-dev"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "agentgrid",
		Short: "Local control plane for parallel coding agents",
		Long: "AgentGrid is a local control plane for coordinating multiple coding\n" +
			"agents across branches: claim-before-touch, stale detection, and\n" +
			"diff-risk scoring.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newInitCmd())
	root.AddCommand(newAgentCmd())
	root.AddCommand(newClaimCmd())
	root.AddCommand(newRefreshCmd())
	root.AddCommand(newStaleCmd())
	root.AddCommand(newDiffRiskCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newVersionCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Println("agentgrid " + version)
		},
	}
}
