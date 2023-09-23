package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Dreamacro/clash/log"
	"github.com/hxnas/hlash/clash"
	"github.com/hxnas/hlash/pkg/cobra"
	"github.com/hxnas/hlash/pkg/svc"
)

var (
	Version     = "unknown"
	Description = "Clash的包装, 支持订阅，预设覆盖，以服务后台运行"
)

const ENV_CLASH_HOME_DIR = "CLASH_HOME_DIR"

func main() {
	cobra.Init(Description, Version)
	cobra.Run(commandRun(), commandSvc())
}

func homeDirFromEnv() string {
	homeDir := os.Getenv(ENV_CLASH_HOME_DIR)
	if homeDir == "" {
		homeDir = "data"
	}
	return homeDir
}

func clashRun(ctx context.Context, homeDir string) {
	log.Infoln("[启动] %s", time.Now().Format(time.RFC3339))
	if err := clash.New(homeDir).Run(ctx); err != nil {
		log.Fatalln("%v", err)
	}
}

func commandRun() *cobra.Command {
	c := &cobra.Command{Use: "run", Short: "运行"}
	c.Flags().StringP("home", "d", homeDirFromEnv(), "数据和配置目录")
	c.Run = func(cmd *cobra.Command, args []string) {
		homeDir, _ := cmd.Flags().GetString("home")
		clashRun(cmd.Context(), homeDir)
	}
	return c
}

func commandSvc() *cobra.Command {
	command := &cobra.Command{Use: "svc", Aliases: []string{"service"}, Short: "服务"}

	run := func(cmd *cobra.Command, args []string) {
		name := cmd.Name()
		homeDir, _ := cmd.Flags().GetString("home")
		homeDir, _ = filepath.Abs(homeDir)

		s := &svc.Service{Name: "hlash"}
		s.Run = func() { clashRun(cmd.Context(), homeDir) }
		s.Arguments = []string{"svc", "run", "-d", homeDir}

		msg, err := s.Control(name)
		if msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	}

	for i, name := range svc.ControlActions {
		c := &cobra.Command{Use: name, Short: svc.ControlLabels[i], Run: run}
		if name == "install" || name == "run" {
			c.Flags().StringP("home", "d", homeDirFromEnv(), "数据和配置目录")
		}
		command.AddCommand(c)
	}

	return command
}
