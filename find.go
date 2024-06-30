package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/google/subcommands"
)

type findCmd struct {
	input  string
	output string
	gaiji  string
}

func (*findCmd) Name() string { return "find" }
func (*findCmd) Synopsis() string {
	return "input ファイルから gaiji ファイルの外字を検索し、該当するデータを output ファイルに出力する"
}
func (*findCmd) Usage() string {
	return `find -i 検索対象ファイル -o 検索結果ファイル -g 外字リストファイル:
	検索対象ファイルから外字リストファイルに定義されている外字を検索し、該当する行を検索結果ファイルに出力する。
`
}

func (p *findCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.input, "i", "", "検索対象ファイルのパス")
	f.StringVar(&p.output, "o", "", "検索結果ファイルのパス")
	f.StringVar(&p.gaiji, "g", "", "外字リストファイルのパス")
}

func (p *findCmd) validate() error {
	if p.input == "" {
		return fmt.Errorf("引数 -i が指定されていません。")
	}
	if p.output == "" {
		return fmt.Errorf("引数 -o が指定されていません。")
	}
	if p.gaiji == "" {
		return fmt.Errorf("引数 -g が指定されていません。")
	}

	return nil
}

func (p *findCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	// 起動時引数のチェック
	if err := p.validate(); err != nil {
		return subcommands.ExitFailure
	}

	// 外字リストファイルを読み込み、外字リスト(gaiji構造体のスライス)を作成する
	gaijiList, err := createGaijiList("gaijilist.txt")
	if err != nil {
		panic(err)
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
