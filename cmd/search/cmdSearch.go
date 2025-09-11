package search

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"Gaijer/cmd"

	"github.com/google/subcommands"
)

// Job はワーカーが処理するタスクを表します。
type Job struct {
	LineNumber int
	Text       string
}

// SearchCmd は 'search' コマンドの構造を定義します。
type SearchCmd struct {
	inputFolder  string
	outputFolder string
	gaijiFile    string
	workerCount  int
	header       bool
	value        bool
}

// channelBufferMultiplier は、ワーカー数に対するチャネルバッファの倍率です。
// ジョブ生成がワーカーの処理を上回る場合に備え、十分なバッファを確保します。
const (
	jobChanBufferMultiplier    = 100
	resultChanBufferMultiplier = 10
)

func (*SearchCmd) Name() string { return "search" }
func (*SearchCmd) Synopsis() string {
	return "指定されたフォルダ内のファイルから外字を検索します。"
}
func (*SearchCmd) Usage() string {
	return `search -i 検索対象フォルダ -o 検索結果出力フォルダ -g 外字リストファイル [-w 並行処理数] [-header] [-value]:
	入力フォルダ内のファイルから外字リストに含まれる文字を検索し、結果を出力フォルダに書き出します。
`
}

func (p *SearchCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.inputFolder, "i", "", "検索対象フォルダのパス")
	f.StringVar(&p.outputFolder, "o", "", "検索結果出力フォルダのパス")
	f.StringVar(&p.gaijiFile, "g", "", "外字リストファイルのパス")
	f.IntVar(&p.workerCount, "w", 4, "並行処理数 (デフォルト: 4)")
	f.BoolVar(&p.header, "header", false, "結果ファイルにヘッダを出力するかどうか")
	f.BoolVar(&p.value, "value", false, "結果ファイルに値（該当行のテキスト）を出力するかどうか")
}

// validate はコマンドライン引数が正しく設定されているか検証します。
func (p *SearchCmd) validate() error {
	if p.inputFolder == "" {
		return fmt.Errorf("引数 -i (入力フォルダ) が指定されていません")
	}
	if p.outputFolder == "" {
		return fmt.Errorf("引数 -o (出力フォルダ) が指定されていません")
	}
	if p.gaijiFile == "" {
		return fmt.Errorf("引数 -g (外字リストファイル) が指定されていません")
	}
	if p.workerCount <= 0 {
		return fmt.Errorf("引数 -w (並行処理数) には1以上の整数を指定してください: %d", p.workerCount)
	}

	return nil
}

// Execute はコマンドのメインロジックを実行します。
func (c *SearchCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	slog.Info("searchコマンドを開始します。")
	var hadError bool
	defer func() {
		if hadError {
			slog.Error("コマンドの実行中に1つ以上のエラーが発生しました。")
		} else {
			slog.Info("全ての処理が正常に完了しました。")
		}
		slog.Info("searchコマンドを終了します。")
	}()

	if err := c.validate(); err != nil {
		slog.Error("引数の検証に失敗しました。", "error", err)
		hadError = true
		return subcommands.ExitUsageError
	}

	gaijiMap, err := c.createGaijiMap()
	if err != nil {
		slog.Error("外字マップの作成に失敗しました。", "error", err)
		hadError = true
		return subcommands.ExitFailure
	}

	files, err := os.ReadDir(c.inputFolder)
	if err != nil {
		slog.Error("入力フォルダの読み取りに失敗しました。", "folder", c.inputFolder, "error", err)
		hadError = true
		return subcommands.ExitFailure
	}

	// 出力フォルダが存在しない場合は作成します。
	if err := os.MkdirAll(c.outputFolder, 0755); err != nil {
		slog.Error("出力フォルダの作成に失敗しました。", "folder", c.outputFolder, "error", err)
		hadError = true
		return subcommands.ExitFailure
	}

	// 取得したファイルごとに処理を実行
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		inputFile := filepath.Join(c.inputFolder, file.Name())
		outputFile := filepath.Join(c.outputFolder, file.Name()+".out")

		slog.Info("ファイルの処理を開始します。", "input", inputFile)
		if err := c.processFile(inputFile, outputFile, gaijiMap); err != nil {
			// 一つのファイルでエラーが発生しても処理を止めず、次のファイルに進みます。
			// エラーはログに出力します。
			slog.Error("ファイルの処理に失敗しました。", "file", inputFile, "error", err)
			hadError = true // エラーがあっても次のファイルの処理を続ける
		} else {
			slog.Info("ファイルの処理が完了しました。", "output", outputFile)
		}
	}

	if hadError {
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

// createGaijiMap は外字リストファイルを読み込み、検索を高速化するためのマップを作成します。
func (c *SearchCmd) createGaijiMap() (map[rune]*cmd.Gaiji, error) {
	gaijiList, err := cmd.CreateGaijiList(c.gaijiFile)
	if err != nil {
		return nil, fmt.Errorf("外字リストファイル '%s' の読み込みに失敗しました: %w", c.gaijiFile, err)
	}

	gaijiMap := make(map[rune]*cmd.Gaiji, len(gaijiList))
	for _, g := range gaijiList {
		gaijiMap[g.Moji] = g
	}
	return gaijiMap, nil
}

// processFile は単一のファイルに対する処理フローを管理します。
func (c *SearchCmd) processFile(inputFile, outputFile string, gaijiMap map[rune]*cmd.Gaiji) error {
	results, err := c.runPipeline(inputFile, gaijiMap)
	if err != nil {
		return fmt.Errorf("処理パイプラインでエラーが発生しました: %w", err)
	}

	// 結果をソートします。
	if err := sortResults(results); err != nil {
		return fmt.Errorf("結果のソートに失敗しました: %w", err)
	}
	if err := cmd.WriteOutputFile(outputFile, results, c.header, c.value); err != nil {
		return fmt.Errorf("結果の出力に失敗しました: %w", err)
	}

	return nil
}

// runPipeline はワーカープールをセットアップし、ファイル処理を実行します。
func (c *SearchCmd) runPipeline(inputFile string, gaijiMap map[rune]*cmd.Gaiji) ([]cmd.Result, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobChan := make(chan Job, c.workerCount*jobChanBufferMultiplier)
	resultChan := make(chan cmd.Result, c.workerCount*resultChanBufferMultiplier)
	errChan := make(chan error, 1)

	var wg sync.WaitGroup
	for i := 1; i <= c.workerCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(ctx, id, jobChan, resultChan, gaijiMap)
		}(i)
	}

	go func() {
		defer close(jobChan)
		if err := createJobs(ctx, inputFile, jobChan); err != nil {
			select {
			case errChan <- err:
			default:
			}
			cancel()
		}
	}()

	// resultChanを閉じるためのgoroutine
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	results := cmd.CollectResults(resultChan)

	// すべての送信元が完了したことが保証された後でerrChanを閉じる。
	// これをしないと、下の for range がデッドロックする可能性がある。
	close(errChan)
	if err := cmd.CheckError(errChan); err != nil {
		// 最初に見つかったエラーを返す
		return nil, err
	}

	return results, nil
}

// sortResults は検索結果をコードポイントと行番号でソートします。
func sortResults(results []cmd.Result) error {
	var sortErr error
	sort.Slice(results, func(i, j int) bool {
		if sortErr != nil {
			return false
		}
		if results[i].Codepoint == results[j].Codepoint {
			// Atoiのエラーを無視せず、適切に処理します。
			iVal, err := strconv.Atoi(results[i].Id)
			if err != nil {
				sortErr = fmt.Errorf("行番号の比較に失敗 (Id: %s): %w", results[i].Id, err)
				return false
			}
			jVal, err := strconv.Atoi(results[j].Id)
			if err != nil {
				sortErr = fmt.Errorf("行番号の比較に失敗 (Id: %s): %w", results[j].Id, err)
				return false
			}
			return iVal < jVal
		}
		return results[i].Codepoint < results[j].Codepoint
	})

	return sortErr
}

// createJobs は入力ファイルを1行ずつ読み込み、ジョブチャネルにタスクを送信します。
func createJobs(ctx context.Context, inputFile string, jobChan chan<- Job) error {
	file, err := os.Open(inputFile)
	if err != nil {
		return err
	}
	defer file.Close()

	// ファイルサイズを取得
	fs, err := file.Stat()
	if err != nil {
		return err
	}
	filesize := fs.Size()
	var readsize int64 // 読み込んだサイズ
	progressRate := -1

	slog.Debug("ジョブの生成を開始します。", "file", inputFile)
	lineNumber := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()

		select {
		case <-ctx.Done():
			slog.Warn("ジョブ生成がキャンセルされました。", "file", inputFile)
			return ctx.Err()
		case jobChan <- Job{LineNumber: lineNumber, Text: line}:
			readsize += int64(len(scanner.Bytes())) + 1
			pr := int((float64(readsize) / float64(filesize)) * 100)
			if progressRate != pr {
				progressRate = pr
				fmt.Fprintf(os.Stderr, "\r入力ファイル読込中 (%s): %3d %%", filepath.Base(inputFile), progressRate)
			}
		}

	}

	fmt.Fprint(os.Stderr, "\r\n")
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("ファイルの読み込み中にエラーが発生しました: %w", err)
	}
	slog.Debug("ジョブの生成が完了しました。", "total_jobs", lineNumber)

	return nil
}

// worker はジョブチャネルからタスクを受け取り、外字検索を実行して結果を送信します。
func worker(ctx context.Context, id int, jobs <-chan Job, results chan<- cmd.Result, gaijiMap map[rune]*cmd.Gaiji) {
	slog.Debug("ワーカーを開始します。", "id", id)
	defer slog.Debug("ワーカーを終了します。", "id", id)

	j := 0
	for job := range jobs {
		j++
		slog.Debug(fmt.Sprintf("[worker] id=%d : processing index=%d", id, j))
		select {
		case <-ctx.Done():
			slog.Warn("ワーカー処理がキャンセルされました。", "id", id)
			return
		default:
			// 1行内で同じ外字を重複して報告しないためのセット
			foundGaijiInLine := make(map[rune]struct{})
			for _, char := range job.Text {
				if g, ok := gaijiMap[char]; ok {
					if _, found := foundGaijiInLine[char]; !found {
						result := cmd.Result{
							Moji:      g.Moji,
							Codepoint: g.Codepoint,
							Id:        strconv.Itoa(job.LineNumber),
							Value:     strings.Trim(job.Text, `"`),
						}
						select {
						case results <- result:
						case <-ctx.Done():
							return
						}
						foundGaijiInLine[char] = struct{}{}
					}
				}
			}

		}
	}

	return nil
}
