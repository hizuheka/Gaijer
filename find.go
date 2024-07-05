package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

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
		slog.Info("END")
	}()

	slog.Info("START")

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
	const numJobs = 5
	jobChan := make(chan string, numJobs)
	resultChan := make(chan Result, numJobs)
	errChan := make(chan error, numJobs)

	// ワーカープール作成
	// - `numWorkers` で指定した数のワーカーを生成する。各ワーカーは`worker`関数を実行するゴルーチンとして起動される
	// - 各ワーカーには一意のID(i)を与え、`jobs`チャネルからタスクを受け取って処理し、その結果を`results`チャネルに送信する
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const numWorkers = 3
	var wg sync.WaitGroup
	for i := 1; i <= numWorkers; i++ {
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
		createJobs(p.input, jobChan, errChan)
	}()

	// ワーカー完了待機
	// - `sync.WaitGroup`を使用して、全てのワーカーの処理が完了するの待ちます。各ワーカーが完了すると`wg.Done`が呼び出され、全てのワーカーが完了すると待機が解除される
	// - 全てのワーカーが完了し、全てのタスクの処理が終わった後、`results`チャネルをクローズする
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 結果の出力
	// - `results`チャネルからタスクの処理結果を受け取り、出力する。
	writeOutputFile(p.output, resultChan, errChan)
	close(errChan)

	// エラー確認
	for e := range errChan {
		if e != nil {
			err = e
			return subcommands.ExitFailure
		}
	}

	return subcommands.ExitSuccess
}

// func extractLinesWithGaiji(gaijiList []*gaiji, inputFile string) ([]Result, error) {
func createJobs(inputFile string, jobChan chan<- string, errChan chan<- error) {
	file, err := os.Open(inputFile)
	if err != nil {
		errChan <- err
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		jobChan <- scanner.Text()
	}

	if err := scanner.Err(); err != nil {
		errChan <- err
		return
	}

	// slog.Info(fmt.Sprintf("入力ファイルから対象データを抽出しました。(抽出件数=%d)", len(results)))
}

// 出力ファイルに書き出す
func writeOutputFile(outputFilePath string, resultChan <-chan Result, errChan chan<- error) {
	// 出力ファイルを開く
	outputFile, err := os.Create(outputFilePath)
	if err != nil {
		errChan <- err
		return
	}
	defer outputFile.Close()

	writer := csv.NewWriter(outputFile)
	// writer.UserCRLF(true)
	defer writer.Flush()

	// ヘッダーを書き込む
	if err := writer.Write([]string{"コード", "文字", "キー", "値"}); err != nil {
		errChan <- err
		return
	}

	// ログエントリを書き込む
	for r := range resultChan {
		if err := writer.Write([]string{fmt.Sprintf("%X", r.moji), string(r.moji), r.key, r.value}); err != nil {
			errChan <- err
			return
		}
	}

	return
}

// ワーカー関数
// - ワーカーが行うタスクの処理
// - ジョブキュー(jobs)からタスクを受け取り、それを処理して、結果を結果チャネル(results)に送信する
func worker(ctx context.Context, i int, jobs <-chan string, results chan<- Result, gaijiList []*gaiji) error {
	slog.Info(fmt.Sprintf("WORKER:%d START", i))
	defer func() {
		slog.Info(fmt.Sprintf("WORKER:%d END", i))
	}()

	for line := range jobs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			a := strings.Split(line, ",")
			if len(a) != 2 {
				return fmt.Errorf("入力ファイルの形式エラー。入力ファイルはカンマ区切り2列を想定。line=%s", line)
			}
			for _, g := range gaijiList {
				if strings.Contains(a[1], string(g.moji)) {
					results <- Result{moji: g.moji, key: a[0], value: a[1]}
				}
			}
		}
	}

	return nil
}
