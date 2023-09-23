package cobra

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"
)

type (
	Command            = cobra.Command
	CompletionOptions  = cobra.CompletionOptions
	FParseErrWhitelist = cobra.FParseErrWhitelist
	Group              = cobra.Group
	PositionalArgs     = cobra.PositionalArgs
	ShellCompDirective = cobra.ShellCompDirective
)

var Description, Version string

func Init(description, version string) {
	Description = description
	Version = version
}

func Run(subs ...*Command) {
	root := &Command{Use: filepath.Base(os.Args[0]), Short: Description, Version: Version}

	root.AddCommand(subs...)
	fixCommand(root, true)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	root.ExecuteContext(ctx)
}

func fixCommand(command *Command, root bool) {
	if root {
		command.CompletionOptions.HiddenDefaultCmd = true
		command.InitDefaultCompletionCmd()
	}

	command.InitDefaultHelpCmd()
	command.InitDefaultHelpFlag()
	command.InitDefaultVersionFlag()

	if hFlag := command.Flags().Lookup("help"); hFlag != nil {
		hFlag.Usage = "帮助"
	}

	if vFlag := command.Flags().Lookup("version"); vFlag != nil {
		vFlag.Usage = "版本"
	}

	for _, sub := range command.Commands() {
		switch sub.Name() {
		case "help":
			sub.Short = "帮助"
		case "completion":
			sub.Short = "生成自动完成脚本"
		default:
			fixCommand(sub, false)
		}
	}
}
