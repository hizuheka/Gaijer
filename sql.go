package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"

	"github.com/google/subcommands"
)

type sqlCmd struct {
	table       string
	sel         string
	where       string
	orderby     string
	output      string
	gaiji       string
	workerCount int
}

func (*sqlCmd) Name() string { return "sql" }
func (*sqlCmd) Synopsis() string {
	return "[table]テーブルから、[gaiji]ファイルと[select]と[where]により生成したSQLを基に抽出した結果を、[output]ファイルに出力する"
}
func (*sqlCmd) Usage() string {
	return `sql -table 検索対象テーブル -select SELECT句 -where WHERE句 [-orderby ORDERBY句] -o 検索結果ファイル -g 外字リストファイル [-w 並行処理数]:
	検索対象テーブルから、外字リストファイルとSELECT句、WHERE句、ORDERBY句から生成したSQLを基に抽出した結果を、検索結果ファイルに出力する
`
}

func (s *sqlCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&s.table, "table", "", "検索対象テーブル")
	f.StringVar(&s.sel, "select", "", "SELECT句")
	f.StringVar(&s.where, "where", "", "WHERE句")
	f.StringVar(&s.orderby, "orderby", "", "ORDERBY句")
	f.StringVar(&s.output, "o", "", "検索結果ファイルのパス")
	f.StringVar(&s.gaiji, "g", "", "外字リストファイルのパス")
	f.IntVar(&s.workerCount, "w", 1, "並行処理数")
}

func (s *sqlCmd) validate() error {
	if s.table == "" {
		return fmt.Errorf("引数 -table が指定されていません。")
	}
	if s.sel == "" {
		return fmt.Errorf("引数 -select が指定されていません。")
	}
	if s.where == "" {
		return fmt.Errorf("引数 -where が指定されていません。")
	}
	if s.output == "" {
		return fmt.Errorf("引数 -o が指定されていません。")
	}
	if s.gaiji == "" {
		return fmt.Errorf("引数 -g が指定されていません。")
	}
	if s.workerCount <= 0 {
		return fmt.Errorf("引数 -w には、1以上の整数を指定してください。(-w=%d)", s.workerCount)
	}

	return nil
}

func (s *sqlCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	var err error
	defer func() {
		if err != nil {
			slog.Error(err.Error())
		}
		slog.Info("END   sql-Command")
	}()

	slog.Info("START sql-Command")

	// 起動時引数のチェック
	if err = s.validate(); err != nil {
		return subcommands.ExitUsageError
	}

	// err := clipboard.Init()
	// if err != nil {
	// 	log.Printf("[clip] %v\n", err)
	// 	return subcommands.ExitFailure
	// }

	// reader := bufio.NewReader(bytes.NewReader(clipboard.Read(clipboard.FmtText)))
	// for i := 0; ; i++ {
	// 	if p.num != 0 && i == p.num {
	// 		break
	// 	}
	// 	line, _, err := reader.ReadLine()
	// 	if err == io.EOF {
	// 		break
	// 	} else if err != nil {
	// 		log.Printf("[clip] %v\n", err)
	// 		return subcommands.ExitFailure
	// 	}

	// 	out := string(line)
	// 	if p.trim {
	// 		out = strings.TrimSpace(out)
	// 	}
	// 	fmt.Println(out)
	// }

	return subcommands.ExitSuccess
}
