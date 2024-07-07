package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/google/subcommands"
)

type Result struct {
	moji      rune
	codepoint string
	key       string
	value     string
}

type findCmd struct {
	input       string
	output      string
	gaiji       string
	workerCount int
}

func (*findCmd) Name() string { return "find" }
func (*findCmd) Synopsis() string {
	return "input ファイルから gaiji ファイルの外字を検索し、該当するデータを output ファイルに出力する"
}
func (*findCmd) Usage() string {
	return `find -i 検索対象ファイル -o 検索結果ファイル -g 外字リストファイル [-w 並行処理数]:
	検索対象ファイルから外字リストファイルに定義されている外字を検索し、該当する行を検索結果ファイルに出力する。
`
}

func (p *findCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.input, "i", "", "検索対象ファイルのパス")
	f.StringVar(&p.output, "o", "", "検索結果ファイルのパス")
	f.StringVar(&p.gaiji, "g", "", "外字リストファイルのパス")
	f.IntVar(&p.workerCount, "w", 1, "並行処理数")
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
	if p.workerCount <= 0 {
		return fmt.Errorf("引数 -w には、1以上の整数を指定してください。(-w=%d)", p.workerCount)
	}

	return nil
}

func (p *findCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	var err error
	defer func() {
		if err != nil {
			slog.Error(err.Error())
		}
		slog.Info("END   find-Command")
	}()

	slog.Info("START find-Command")

	// 起動時引数のチェック
	if err = p.validate(); err != nil {
		return subcommands.ExitUsageError
	}

	// 外字リストファイルを読み込み、外字リスト(gaiji構造体のスライス)を作成する
	var gaijiList []*gaiji
	gaijiList, err = createGaijiList(p.gaiji)
	if err != nil {
		return subcommands.ExitFailure
	}

	// タスク準備
	// - `numJobs` で指定した数だけのタスクを処理するため、ジョブキュー (`jobs`)と結果を格納するチャネル(`results`)を準備する
	// - タスクの数に合わせてバッファサイズを設定する
	jobChan := make(chan string, p.workerCount*100)
	resultChan := make(chan Result, p.workerCount*10)
	errChan := make(chan error, p.workerCount)

	// ワーカープール作成
	// - `numWorkers` で指定した数のワーカーを生成する。各ワーカーは`worker`関数を実行するゴルーチンとして起動される
	// - 各ワーカーには一意のID(i)を与え、`jobs`チャネルからタスクを受け取って処理し、その結果を`results`チャネルに送信する
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for i := 1; i <= p.workerCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := worker(ctx, i, jobChan, resultChan, gaijiList); err != nil {
				cancel()
				errChan <- err
			}
		}(i)
	}

	// タスク割り当て
	// - タスク(1からnumJobsまでの数値)をjobsチャネルに送信し、ワーカーに処理させる
	// - 全てのタスクが`jobs`チャネルに送信された後、`close(jobs)`によりチャネルをクローズする。これにより、追加のタスクがないことがワーカーに通知される
	go func() {
		defer close(jobChan)
		if err := createJobs(ctx, p.input, jobChan); err != nil {
			cancel()
			errChan <- err
		}
	}()

	// 結果の集約
	done := make(chan struct{})
	var results []Result
	go func() {
		defer close(done)
		results = collectResults(resultChan)
	}()

	// ワーカー完了待機
	// - `sync.WaitGroup`を使用して、全てのワーカーの処理が完了するの待ちます。各ワーカーが完了すると`wg.Done`が呼び出され、全てのワーカーが完了すると待機が解除される
	// - 全てのワーカーが完了し、全てのタスクの処理が終わった後、`results`チャネルをクローズする
	go func() {
		slog.Debug("[wg.Wait] START")
		wg.Wait()
		close(errChan)
		close(resultChan)
		slog.Debug("[wg.Wait] END")
	}()

	// 完了確認
	<-done

	// エラー確認
	err = checkError(errChan)
	if err != nil {
		return subcommands.ExitFailure
	}

	// 結果のソート
	sort.Slice(results, func(i, j int) bool {
		if results[i].codepoint == results[j].codepoint {
			return results[i].key < results[j].key
		}
		return results[i].codepoint < results[j].codepoint
	})

	// 結果の出力
	// - `results`チャネルからタスクの処理結果を受け取り、出力する。
	err = writeOutputFile(p.output, results)
	if err != nil {
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}

// func extractLinesWithGaiji(gaijiList []*gaiji, inputFile string) ([]Result, error) {
func createJobs(ctx context.Context, inputFile string, jobChan chan<- string) error {
	slog.Debug("[createJobs] START")
	i := 0
	defer func() {
		slog.Info(fmt.Sprintf("[createJobs] END : 生成したジョブの数=%d", i))
	}()

	file, err := os.Open(inputFile)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			slog.Debug("[createJobs] canceled")
			return nil
		default:
			jobChan <- scanner.Text()
			i++
			slog.Debug(fmt.Sprintf("[createJobs] add : job %d", i))
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

// 出力ファイルに書き出す
func writeOutputFile(outputFilePath string, results []Result) error {
	slog.Debug("[writeOutputFile] START")
	i := 0
	defer func() {
		slog.Info(fmt.Sprintf("[writeOutputFile] END : 出力した行数(ヘッダ除く)=%d", i))
	}()

	// 出力ファイルを開く
	outputFile, err := os.Create(outputFilePath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	writer := csv.NewWriter(outputFile)
	// writer.UserCRLF(true)
	defer writer.Flush()

	// ヘッダーを書き込む
	if err := writer.Write([]string{"コード", "文字", "キー", "値"}); err != nil {
		return err
	}

	// ログエントリを書き込む
	for _, r := range results {
		if err := writer.Write([]string{r.codepoint, string(r.moji), r.key, r.value}); err != nil {
			return err
		}
		i++
	}

	return nil
}

// ワーカー関数
// - ワーカーが行うタスクの処理
// - ジョブキュー(jobs)からタスクを受け取り、それを処理して、結果を結果チャネル(results)に送信する
func worker(ctx context.Context, id int, jobs <-chan string, results chan<- Result, gaijiList []*gaiji) error {
	slog.Debug(fmt.Sprintf("[worker] id=%d : START", id))
	defer func() {
		slog.Debug(fmt.Sprintf("[worker] id=%d : END", id))
	}()

	j := 0
	for line := range jobs {
		j++
		slog.Debug(fmt.Sprintf("[worker] id=%d : processing index=%d", id, j))
		select {
		case <-ctx.Done():
			slog.Debug(fmt.Sprintf("[worker] id=%d : canceled", id))
			return ctx.Err()
		default:
			a := strings.Split(line, ",")
			if len(a) != 2 {
				slog.Error(fmt.Sprintf("[worker] id=%d : ERROR!!", id))
				return fmt.Errorf("入力ファイルの形式エラー。入力ファイルはカンマ区切り2列を想定。line=%s", line)
			}
			for _, g := range gaijiList {
				if strings.Contains(a[1], string(g.moji)) {
					results <- Result{moji: g.moji, codepoint: g.codepoint, key: a[0], value: a[1]}
				}
			}
		}
	}

	return nil
}

func collectResults(resultChan <-chan Result) []Result {
	slog.Debug("[collectResults] START")

	var results []Result
	i := 0
	for r := range resultChan {
		results = append(results, r)
		i++
		slog.Debug(fmt.Sprintf("[collectResults] add index=%d", i))
	}

	slog.Debug("[collectResults] END")
	return results
}

func checkError(errChan <-chan error) error {
	slog.Debug("[error check] START")
	select {
	case e, ok := <-errChan:
		if ok { // エラーを取得できた場合。closeされていた場合は、okはfalseになる
			slog.Error("[error check] END : ERROR!!")
			return e
		}
	default:
		slog.Error("[error check] errChanがcloseされていません")
		return fmt.Errorf("errChanがcloseされていません")
	}

	slog.Debug("[error check] END : NO ERROR")
	return nil
}
