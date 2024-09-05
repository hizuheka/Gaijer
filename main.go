package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"Gaijer/cmd/find"

	"github.com/google/subcommands"
)

var version string

func main() {
	subcommands.Register(subcommands.HelpCommand(), "")
	subcommands.Register(subcommands.FlagsCommand(), "")
	subcommands.Register(subcommands.CommandsCommand(), "")
	subcommands.Register(&find.FindCmd{}, "")

	isDebug := flag.Bool("d", false, "debugログを出力")
	isVersion := flag.Bool("v", false, "バージョンを出力")
	flag.Parse()

	// バージョン出力
	if *isVersion {
		fmt.Printf("Gaijer.exe version %s\n", version)
		os.Exit(int(subcommands.ExitSuccess))
	}

	// ログレベルの設定
	switch {
	case *isDebug:
		slog.SetLogLoggerLevel(slog.LevelDebug)
	default:
		slog.SetLogLoggerLevel(slog.LevelInfo)
	}
	ctx := context.Background()

	os.Exit(int(subcommands.Execute(ctx)))
}
