package memory

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	"github.com/sipeed/picoclaw/pkg/agent"
)

// NewMemoryCommand backs `picoclaw memory backup|list|restore`.
func NewMemoryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Back up and restore local agent memory",
		Long: "Back up the agent's memory directory to a zip archive outside the workspace, " +
			"list existing archives, or restore one. Restoring overwrites current memory " +
			"after saving a per-file safety copy.",
	}

	cmd.AddCommand(newBackupCommand(), newListCommand(), newRestoreCommand())
	return cmd
}

func newBackupCommand() *cobra.Command {
	var includeConfig bool
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create an archive of the memory directory",
		Args:  cobra.NoArgs,
		Example: `  picoclaw memory backup
  picoclaw memory backup --include-config`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := internal.LoadConfig()
			if err != nil {
				return err
			}
			mem := cfg.Agents.Defaults.Memory.Backup
			path, err := agent.CreateMemoryArchive(
				cfg.WorkspacePath(),
				mem.Dir,
				internal.GetConfigPath(),
				includeConfig,
				mem.EffectiveKeep(),
			)
			if err != nil {
				return err
			}
			fmt.Printf("Archive written: %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&includeConfig, "include-config", false,
		"Also archive config.json (contains secrets; stored inside the zip)")
	return cmd
}

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List existing memory archives (newest first)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := internal.LoadConfig()
			if err != nil {
				return err
			}
			archives, err := agent.ListMemoryArchives(cfg.Agents.Defaults.Memory.Backup.Dir)
			if err != nil {
				return err
			}
			if len(archives) == 0 {
				fmt.Println("No memory archives found.")
				return nil
			}
			for _, a := range archives {
				fmt.Printf("%s  %8d bytes  %s\n", a.ModTime.Format(time.RFC3339), a.Size, a.Path)
			}
			return nil
		},
	}
}

func newRestoreCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "restore <archive.zip>",
		Short: "Restore an archive over the workspace memory",
		Args:  cobra.ExactArgs(1),
		Example: `  picoclaw memory list
  picoclaw memory restore ~/.picoclaw/backups/picoclaw-memory-20260101-120000.zip --force`,
		RunE: func(_ *cobra.Command, args []string) error {
			if !force {
				return fmt.Errorf("restoring overwrites current memory; re-run with --force to confirm")
			}
			cfg, err := internal.LoadConfig()
			if err != nil {
				return err
			}
			if err := agent.RestoreMemoryArchive(args[0], cfg.WorkspacePath(), internal.GetConfigPath()); err != nil {
				return err
			}
			fmt.Println("Archive restored. Previous files saved as <file>.bak.<timestamp>.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Confirm overwriting current memory")
	return cmd
}
