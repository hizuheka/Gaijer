package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

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

// 入力ファイルを処理し、出力ファイルに書き出す
func processInputFile(inputFilePath, outputFilePath string, gaijiList []*gaiji) error {
	inputFile, err := os.Open(inputFilePath)
	if err != nil {
		return err
	}
	defer inputFile.Close()

	// ファイルサイズ
	fi, err := inputFile.Stat()
	if err != nil {
		return err
	}
	filesize := fi.Size()

	outputFile, err := os.Create(outputFilePath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	r := bufio.NewReader(inputFile)
	writer := bufio.NewWriter(outputFile)
	defer writer.Flush()

	var c int64 = 0
	var oldP int64 = 0
	for {
		line, err := r.ReadString('\n') // LF(\n)まで読み込むので、CRLF(\r\n)でも問題なし
		if err != nil && err != io.EOF {
			return err
		}
		// 最終行に改行がない場合を考慮し、len(row) == 0 を入れる
		if err == io.EOF && len(line) == 0 {
			break
		}

		for _, char := range line {
			if searchChars[char] {
				return true
			}
		}
		return false
		if containsSearchChar(line, gaijiList) {
			_, err := writer.WriteString(line)
			if err != nil {
				return err
			}
		}

		c = c + int64(len(line))
		p := c / (filesize / 100)
		if p != oldP {
			fmt.Printf("\rReading: %2d%%", p)
			// fmt.Printf("Reading: %2d%%\n", p)
			oldP = p
		}
	}

	fmt.Printf("\nfile size=%d, read size=%d", filesize, c)

	return nil
}

// 行に検索対象文字が含まれているかチェックする
func containsSearchChar(line string, searchChars map[rune]bool) bool {
	for _, char := range line {
		if searchChars[char] {
			return true
		}
	}
	return false
}

type rule struct {
	Character string
}

type Result struct {
	Character string
	Line      string
}

func extractLinesWithGaiji(gaijiList []rule, inputFile string) ([]Result, error) {
	var results []Result

	file, err := os.Open(inputFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		for _, r := range gaijiList {
			if contains(line, r.Character) {
				results = append(results, Result{Character: r.Character, Line: line})
				break
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

func contains(line, gaiji string) bool {
	return len(gaiji) > 0 && len(line) >= len(gaiji) && (line == gaiji || (len(line) > len(gaiji) && (contains(line[1:], gaiji) || contains(line[:len(line)-1], gaiji))))
}
