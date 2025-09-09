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

type SearchCmd struct {
	inputFolder  string
	outputFolder string
	gaiji        string
	workerCount  int
	header       bool
	value        bool
}

func (*SearchCmd) Name() string { return "search" }
func (*SearchCmd) Synopsis() string {
	return "入力フォルダから gaiji ファイルの外字を検索し、該当するデータを指定された出力フォルダに出力する"
}
func (*SearchCmd) Usage() string {
	return `search -i 検索対象フォルダ -o 検索結果出力フォルダ -g 外字リストファイル [-w 並行処理数] [-header] [-value]:
	検索対象フォルダ内の各ファイルから外字リストファイルに定義されている外字を検索し、該当する行を検索結果出力フォルダに出力する。
`
}

func (p *SearchCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.inputFolder, "i", "", "検索対象フォルダのパス")
	f.StringVar(&p.outputFolder, "o", "", "検索結果出力フォルダのパス")
	f.StringVar(&p.gaiji, "g", "", "外字リストファイルのパス")
	f.IntVar(&p.workerCount, "w", 1, "並行処理数")
	f.BoolVar(&p.header, "header", false, "ヘッダの出力有無")
	f.BoolVar(&p.value, "value", false, "値の出力有無")
}

func (p *SearchCmd) validate() error {
	if p.inputFolder == "" {
		return fmt.Errorf("引数 -i が指定されていません。")
	}
	if p.outputFolder == "" {
		return fmt.Errorf("引数 -o が指定されていません。")
	}
	if p.gaiji == "" {
		return fmt.Errorf("引数 -g が指定されていません。")
	}
	if p.workerCount <= 0 {
		return fmt.Errorf("引数 -w には、1以上の整数を指定してください。(-w=%d)", p.workerCount)
	}

	return nil
}

func (c *SearchCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	slog.Info("searchコマンドを開始します。")
	var finalErr error
	defer func() {
		if finalErr != nil {
			slog.Error("コマンドの実行中にエラーが発生しました。", "error", finalErr)
		}
		slog.Info("searchコマンドを終了します。")
	}()

	// 起動時引数のチェック
	if err := c.validate(); err != nil {
		finalErr = err
		return subcommands.ExitUsageError
	}

	// 外字リストを読み込み、検索を高速化するためruneをキーとするマップに変換します。
	gaijiMap, err := c.createGaijiMap()
	if err != nil {
		finalErr = err
		return subcommands.ExitFailure
	}

	// 入力フォルダ内のファイルリストを取得します。
	files, err := os.ReadDir(c.inputFolder)
	if err != nil {
		finalErr = fmt.Errorf("入力フォルダの読み取りに失敗しました: %w", err)
		return subcommands.ExitFailure
	}

	// 出力フォルダが存在しない場合は作成します。
	// os.ModePerm (0777) は過剰な権限のため、より安全な 0755 を使用します。
	if err := os.MkdirAll(c.outputFolder, 0755); err != nil {
		finalErr = fmt.Errorf("出力フォルダの作成に失敗しました: %w", err)
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
			finalErr = err // 最終的なエラーとして記憶
		} else {
			slog.Info("ファイルの処理が完了しました。", "output", outputFile)
		}
	}

	if finalErr != nil {
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

// createGaijiMap は外字リストファイルを読み込み、検索用のマップを作成します。
func (c *SearchCmd) createGaijiMap() (map[rune]*cmd.Gaiji, error) {
	gaijiList, err := cmd.CreateGaijiList(c.gaiji)
	if err != nil {
		return nil, fmt.Errorf("外字リストファイルの読み込みに失敗しました (%s): %w", c.gaiji, err)
	}

	gaijiMap := make(map[rune]*cmd.Gaiji, len(gaijiList))
	for _, g := range gaijiList {
		gaijiMap[g.Moji] = g
	}
	return gaijiMap, nil
}

// processFile は単一のファイルに対する検索処理全体を管理します。
func (c *SearchCmd) processFile(inputFile, outputFile string, gaijiMap map[rune]*cmd.Gaiji) error {
	ctx, cancel := context.WithCancel(context.Background())
	// deferではなく、関数の最後に明示的にcancel()を呼び出すことで、意図を明確にします。
	defer cancel()

	// タスク準備
	// - ジョブキューを管理するチャネル (`jobChan`)を準備する。バッファは適当・・・
	// - 結果を格納するチャネル(`resultChan`)を準備する。バッファは適当・・・
	// - 発生したエラーを確認するためのチャネル(`errChan`)を準備する
	jobChan := make(chan Job, c.workerCount*100)
	resultChan := make(chan cmd.Result, c.workerCount*10)
	errChan := make(chan error, c.workerCount+1) // ジョブ生成元とワーカーからのエラーを受信

	// ワーカープール作成
	// - `p.workerCount` で指定した数のワーカーを生成する。各ワーカーは`worker`関数を実行するゴルーチンとして起動される
	// - 各ワーカーには一意のID(i)を与え、`jobChan`チャネルからタスクを受け取って処理し、その結果を`resultChan`チャネルに送信する
	// - ワーカーでエラーが発生した場合は、`errChan`チャネルに送信する
	var wg sync.WaitGroup
	for i := 1; i <= c.workerCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := worker(ctx, id, jobChan, resultChan, gaijiMap); err != nil {
				cancel()
				errChan <- err
			}
		}(i)
	}

	// タスク割り当て
	// - `p.input`ファイルを読み込み、`jobChan`チャネルに送信し、ワーカーに処理させる
	// - `p.input`の全ての行が`jobChan`チャネルに送信された後、`close(jobChan)`によりチャネルをクローズする。
	// - これにより、追加のタスクがないことがワーカーに通知される
	go func() {
		defer close(jobChan)
		if err := createJobs(ctx, inputFile, jobChan); err != nil {
			errChan <- fmt.Errorf("ジョブの生成に失敗しました: %w", err)
			cancel() // エラー発生時に他のゴルーチンをキャンセル
		}
	}()

	// 全てのワーカーが完了したら、結果チャネルを閉じます。
	// ワーカー完了待機
	// - `sync.WaitGroup`を使用して、全てのワーカーの処理が完了するの待つ
	// - 各ワーカーが完了すると`wg.Done`が呼び出され、全てのワーカーが完了すると待機が解除される
	// - 全てのワーカーが完了し、全てのタスクの処理が終わった後、`resultChan`チャネルと`errChan`チャネルをクローズする
	// - `resultChan`チャネルをクローズすることで、結果の集約処理が終了する。
	go func() {
		slog.Debug("[wg.Wait] START")
		wg.Wait()
		close(resultChan)
		close(errChan) // ここでerrChanを安全に閉じます
		slog.Debug("[wg.Wait] END")
	}()

	// 結果の集約
	// - `resultChan`チャネルがcloseするまで受信し、`results`に追加する
	// - `resultChan`チャネルからの受信後は、`close(done)`によりチャネルをクローズする
	// - これにより、結果の集約が完了したことを通知する
	results := cmd.CollectResults(resultChan)

	// エラーチャネルをチェックします。
	// createJobsからエラーが送られてくる可能性があるため、先にチェックします。
	if err := cmd.CheckError(errChan); err != nil {
		return err
	}

	// 結果をソートします。
	if err := sortResults(results); err != nil {
		return fmt.Errorf("結果のソートに失敗しました: %w", err)
	}

	// 結果をファイルに出力します。
	if err := cmd.WriteOutputFile(outputFile, results, c.header, c.value); err != nil {
		return fmt.Errorf("結果の出力に失敗しました: %w", err)
	}

	return nil
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

	slog.Info(fmt.Sprintf("[sortResults] END sort.Slice : 抽出結果=%d", len(results)))

	return sortErr
}

// createJobs は入力ファイルを読み込み、ジョブチャネルにタスクを送信します。
func createJobs(ctx context.Context, inputFile string, jobChan chan<- Job) error {
	slog.Debug("[createJobs] START")
	lineNumber := 0
	defer func() {
		fmt.Println()
		slog.Info("[createJobs] ジョブの生成が完了しました。", "total_jobs", lineNumber)
	}()

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
	progressRate := 0  // 進捗率

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineNumber++
		select {
		case <-ctx.Done():
			slog.Info("[createJobs] ジョブ生成がキャンセルされました。")
			return nil
			// default:
		case jobChan <- Job{LineNumber: lineNumber, Text: scanner.Text()}:
			readsize = readsize + int64(len(scanner.Bytes())) + 1 // +1 は改行コード分
			pr := int((float64(readsize) / float64(filesize)) * 100)
			// 進捗率(整数)が変化した場合のみ、コンソールに表示
			if progressRate != pr {
				progressRate = pr
				fmt.Fprintf(os.Stderr, "\r入力ファイル読込状況： %d %%", progressRate)
			}
			slog.Debug("[createJobs] ジョブを追加しました。", "add_job", lineNumber)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

// ワーカー関数
// - ワーカーが行うタスクの処理
// - ジョブキュー(jobs)からタスクを受け取り、それを処理して、結果を結果チャネル(results)に送信する
func worker(ctx context.Context, id int, jobs <-chan Job, results chan<- cmd.Result, gaijiMap map[rune]*cmd.Gaiji) error {
	slog.Debug(fmt.Sprintf("[worker] id=%d : START", id))
	defer func() {
		slog.Debug(fmt.Sprintf("[worker] id=%d : END", id))
	}()

	j := 0
	for job := range jobs {
		j++
		slog.Debug(fmt.Sprintf("[worker] id=%d : processing index=%d", id, j))
		select {
		case <-ctx.Done():
			slog.Info("[worker] ワーカー処理がキャンセルされました。", "id", id)
			return ctx.Err()
		default:
			// 1行内で同じ外字を重複して報告しないためのセット
			foundGaiji := make(map[rune]struct{})
			for _, char := range job.Text {
				if g, ok := gaijiMap[char]; ok {
					if _, found := foundGaiji[char]; !found {
						results <- cmd.Result{
							Moji:      g.Moji,
							Codepoint: g.Codepoint,
							Id:        strconv.Itoa(job.LineNumber),
							Value:     strings.Trim(job.Text, "\""),
						}
						foundGaiji[char] = struct{}{}
					}
				}
			}
		}
	}

	return nil
}
