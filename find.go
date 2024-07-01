package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/subcommands"
)

type Result struct {
	moji  rune
	key   string
	value string
}

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
	var err error
	defer func() {
		if err != nil {
			slog.Error(err.Error())
		}
	}()

	// 起動時引数のチェック
	if err = p.validate(); err != nil {
		return subcommands.ExitUsageError
	}

	// 外字リストファイルを読み込み、外字リスト(gaiji構造体のスライス)を作成する
	var gaijiList []*gaiji
	gaijiList, err = createGaijiList("gaijilist.txt")
	if err != nil {
		return subcommands.ExitFailure
	}

	var results []Result
	results, err = extractLinesWithGaiji(gaijiList, p.input)
	if err != nil {
		return subcommands.ExitFailure
	}

	err = writeFile(p.output, results)
	if err != nil {
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}

func extractLinesWithGaiji(gaijiList []*gaiji, inputFile string) ([]Result, error) {
	var results []Result

	file, err := os.Open(inputFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		a := strings.Split(line, ",")
		if len(a) != 2 {
			return nil, fmt.Errorf("入力ファイルの形式エラー。入力ファイルはカンマ区切り2列を想定。line=%s", line)
		}
		for _, g := range gaijiList {
			if strings.Contains(a[1], string(g.moji)) {
				results = append(results, Result{moji: g.moji, key: a[0], value: a[1]})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// 出力ファイルに書き出す
func writeFile(outputFilePath string, results []Result) error {
	outputFile, err := os.Create(outputFilePath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	w := bufio.NewWriter(outputFile)
	defer w.Flush()

	for _, r := range results {
		_, err := w.WriteString(fmt.Sprintf("%s,%s,%s", r.moji, r.key, r.value))
		if err != nil {
			return err
		}
	}

	return nil
}
