package find

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

	"Gaijer/cmd"

	"github.com/google/subcommands"
)

type FindCmd struct {
	input       string
	output      string
	gaiji       string
	workerCount int
	header      bool
	value       bool
}

func (*FindCmd) Name() string { return "find" }
func (*FindCmd) Synopsis() string {
	return "input ファイルから gaiji ファイルの外字を検索し、該当するデータを output ファイルに出力する"
}
func (*FindCmd) Usage() string {
	return `find -i 検索対象ファイル -o 検索結果ファイル -g 外字リストファイル [-w 並行処理数] [-header] [-value]:
	検索対象ファイルから外字リストファイルに定義されている外字を検索し、該当する行を検索結果ファイルに出力する。
`
}

func (p *FindCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.input, "i", "", "検索対象ファイルのパス")
	f.StringVar(&p.output, "o", "", "検索結果ファイルのパス")
	f.StringVar(&p.gaiji, "g", "", "外字リストファイルのパス")
	f.IntVar(&p.workerCount, "w", 1, "並行処理数")
	f.BoolVar(&p.header, "header", false, "ヘッダの出力有無")
	f.BoolVar(&p.value, "value", false, "値の出力有無")
}

func (p *FindCmd) validate() error {
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

func (p *FindCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	var err error
	defer func() {
		if err != nil {
			slog.Error(err.Error())
		}
		slog.Info("END find-Command")
	}()

	slog.Info("START find-Command")

	// 起動時引数のチェック
	if err = p.validate(); err != nil {
		return subcommands.ExitUsageError
	}

	// 外字リストファイルを読み込み、外字リスト(gaiji構造体のスライス)を作成する
	var gaijiList []*cmd.Gaiji
	gaijiList, err = cmd.CreateGaijiList(p.gaiji)
	if err != nil {
		return subcommands.ExitFailure
	}

	// タスク準備
	// - ジョブキューを管理するチャネル (`jobChan`)を準備する。バッファは適当・・・
	// - 結果を格納するチャネル(`resultChan`)を準備する。バッファは適当・・・
	// - 発生したエラーを確認するためのチャネル(`errChan`)を準備する
	jobChan := make(chan string, p.workerCount*100)
	resultChan := make(chan cmd.Result, p.workerCount*10)
	errChan := make(chan error, p.workerCount)

	// ワーカープール作成
	// - `p.workerCount` で指定した数のワーカーを生成する。各ワーカーは`worker`関数を実行するゴルーチンとして起動される
	// - 各ワーカーには一意のID(i)を与え、`jobChan`チャネルからタスクを受け取って処理し、その結果を`resultChan`チャネルに送信する
	// - ワーカーでエラーが発生した場合は、`errChan`チャネルに送信する
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
	// - `p.input`ファイルを読み込み、`jobChan`チャネルに送信し、ワーカーに処理させる
	// - `p.input`の全ての行が`jobChan`チャネルに送信された後、`close(jobChan)`によりチャネルをクローズする。
	// - これにより、追加のタスクがないことがワーカーに通知される
	go func() {
		defer close(jobChan)
		if err := createJobs(ctx, p.input, jobChan); err != nil {
			cancel()
			errChan <- err
		}
	}()

	// 結果の集約
	// - `resultChan`チャネルがcloseするまで受信し、`results`に追加する
	// - `resultChan`チャネルからの受信後は、`close(done)`によりチャネルをクローズする
	// - これにより、結果の集約が完了したことを通知する
	done := make(chan struct{})
	var results []cmd.Result
	go func() {
		defer close(done)
		results = collectResults(resultChan)
	}()

	// ワーカー完了待機
	// - `sync.WaitGroup`を使用して、全てのワーカーの処理が完了するの待つ
	// - 各ワーカーが完了すると`wg.Done`が呼び出され、全てのワーカーが完了すると待機が解除される
	// - 全てのワーカーが完了し、全てのタスクの処理が終わった後、`resultChan`チャネルと`errChan`チャネルをクローズする
	// - `resultChan`チャネルをクローズすることで、結果の集約処理が終了する。
	go func() {
		slog.Debug("[wg.Wait] START")
		wg.Wait()
		close(errChan)
		close(resultChan)
		slog.Debug("[wg.Wait] END")
	}()

	// 完了確認
	// - 結果の集約処理が完了すると、`close(done)`されるため、待機が解除される
	<-done

	// エラー確認
	// goroutineでエラーが発生していなかを、`errChan`チャネルでチェックする
	err = checkError(errChan)
	if err != nil {
		return subcommands.ExitFailure
	}

	// 結果のソート
	// - コードポイント(昇順) > 識別番号(昇順)
	sort.Slice(results, func(i, j int) bool {
		if results[i].Codepoint == results[j].Codepoint {
			return results[i].Id < results[j].Id
		}
		return results[i].Codepoint < results[j].Codepoint
	})

	// 結果の出力
	// - `result`の内容を`p.output`に出力する。
	err = writeOutputFile(p.output, results, p.header, p.value)
	if err != nil {
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}

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
func writeOutputFile(outputFilePath string, results []cmd.Result, isOutputHeader bool, isOutputValue bool) error {
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
	writer.UseCRLF = true
	defer writer.Flush()

	// ヘッダーを書き込む
	if isOutputHeader {
		if err := writer.Write(cmd.ResultHeaderAry()); err != nil {
			return err
		}
	}

	// ログエントリを書き込む
	for _, r := range results {
		if err := writer.Write(r.Csv(isOutputValue)); err != nil {
			return err
		}
		i++
	}

	return nil
}

// ワーカー関数
// - ワーカーが行うタスクの処理
// - ジョブキュー(jobs)からタスクを受け取り、それを処理して、結果を結果チャネル(results)に送信する
func worker(ctx context.Context, id int, jobs <-chan string, results chan<- cmd.Result, gaijiList []*cmd.Gaiji) error {
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
			if len(a) != 3 {
				slog.Error(fmt.Sprintf("[worker] id=%d : ERROR!!", id))
				return fmt.Errorf("入力ファイルの形式エラー。入力ファイルはカンマ区切り3列を想定。line=%s", line)
			}
			for _, g := range gaijiList {
				if strings.Contains(a[2], string(g.Moji)) {
					results <- cmd.Result{Moji: g.Moji, Codepoint: g.Codepoint, Id: a[0], Attr: a[1], Value: a[2]}
				}
			}
		}
	}

	return nil
}

func collectResults(resultChan <-chan cmd.Result) []cmd.Result {
	slog.Debug("[collectResults] START")

	var results []cmd.Result
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
