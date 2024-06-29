package main

import (
	"context"
	"flag"

	"github.com/google/subcommands"
)

type sqlCmd struct {
	table  string
	sel    string
	where  string
	output string
	gaiji  string
}

func (*sqlCmd) Name() string { return "sql" }
func (*sqlCmd) Synopsis() string {
	return "[table]テーブルから、[gaiji]ファイルと[select]と[where]により生成したSQLを基に抽出した結果を、[output]ファイルに出力する"
}
func (*sqlCmd) Usage() string {
	return `sql -t 検索対象テーブル -s SELECT句 -w WHERE句 -o 検索結果ファイル -g 外字リストファイル:
	検索対象テーブルから、外字リストファイルとSELECT句とWHERE句から生成したSQLを基に抽出した結果を、検索結果ファイルに出力する
`
}

func (p *sqlCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.table, "t", "", "検索対象テーブル")
	f.StringVar(&p.sel, "s", "", "SELECT句")
	f.StringVar(&p.where, "w", "", "WHERE句")
	f.StringVar(&p.output, "o", "", "検索結果ファイルのパス")
	f.StringVar(&p.gaiji, "g", "", "外字リストファイルのパス")
}

func (p *sqlCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
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
