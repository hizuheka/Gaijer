package main

import (
	"context"
	"flag"

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

func (p *findCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
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
