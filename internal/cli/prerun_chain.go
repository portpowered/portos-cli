package cli

import "github.com/spf13/cobra"

// PreRunChain composes multiple PersistentPreRun hooks into a single function.
// Hooks execute in the order provided. Nil hooks are safely skipped.
func PreRunChain(hooks ...func(cmd *cobra.Command, args []string)) func(cmd *cobra.Command, args []string) {
	return func(cmd *cobra.Command, args []string) {
		for _, hook := range hooks {
			if hook != nil {
				hook(cmd, args)
			}
		}
	}
}
