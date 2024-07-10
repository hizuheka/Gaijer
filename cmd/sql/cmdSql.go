package unlsql

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"Gaijer/cmd"

	"github.com/google/subcommands"
)

type job struct {
	moji      rune
	codepoint string
	sql       string
}

type UnlsqlCmd struct {
	db          string
	sql         string
	output      string
	gaiji       string
	workerCount int
	header      bool
	value       bool
}

func (*UnlsqlCmd) Name() string { return "sql" }
func (*UnlsqlCmd) Synopsis() string {
	return "[db]から、[gaiji]ファイルと[sql]基に抽出した結果を、[output]ファイルに出力する"
}
func (*UnlsqlCmd) Usage() string {
	return `unlsql -db DB -sql SQL -o 検索結果ファイル -g 外字リストファイル [-w 並行処理数] [-header] [-value]:
	DBから、外字リストファイルとSQLを基に抽出した結果を、検索結果ファイルに出力する
`
}

func (s *UnlsqlCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&s.db, "db", "", "DB")
	f.StringVar(&s.sql, "sql", "", "SQL")
	f.StringVar(&s.output, "o", "", "検索結果ファイルのパス")
	f.StringVar(&s.gaiji, "g", "", "外字リストファイルのパス")
	f.IntVar(&s.workerCount, "w", 1, "並行処理数")
	f.BoolVar(&s.header, "header", false, "ヘッダの出力有無")
	f.BoolVar(&s.value, "value", false, "値の出力有無")
}

func (s *UnlsqlCmd) validate() error {
	if s.db == "" {
		return fmt.Errorf("引数 -db が指定されていません。")
	}
	if s.sql == "" {
		return fmt.Errorf("引数 -sql が指定されていません。")
	}
	if !strings.Contains(s.sql, "%s") {
		return fmt.Errorf("引数 -sql には、外字文字置換箇所%sの指定が必要です。")
	}
	if s.output == "" {
		return fmt.Errorf("引数 -o が指定されていません。")
	}
	if s.gaiji == "" {
		return fmt.Errorf("引数 -g が指定されていません。")
	}
	if s.workerCount <= 0 {
		return fmt.Errorf("引数 -w には、1以上の整数を指定してください。(-w=%d)", s.workerCount)
	}

	return nil
}

func (s *UnlsqlCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	var err error
	defer func() {
		if err != nil {
			slog.Error(err.Error())
		}
		slog.Info("END   sql-Command")
	}()

	slog.Info("START sql-Command")

	// 起動時引数のチェック
	if err = s.validate(); err != nil {
		return subcommands.ExitUsageError
	}

	// 外字リストファイルを読み込み、外字リスト(gaiji構造体のスライス)を作成する
	var gaijiList []*cmd.Gaiji
	gaijiList, err = cmd.CreateGaijiList(s.gaiji)
	if err != nil {
		return subcommands.ExitFailure
	}

	// タスク準備
	// - ジョブキューを管理するチャネル (`jobChan`)を準備する。バッファは適当・・・
	// - 結果を格納するチャネル(`resultChan`)を準備する。バッファは適当・・・
	// - 発生したエラーを確認するためのチャネル(`errChan`)を準備する
	jobChan := make(chan string, 3000)
	resultChan := make(chan cmd.Result, s.workerCount*10)
	errChan := make(chan error, s.workerCount)

	// ワーカープール作成
	// - `p.workerCount` で指定した数のワーカーを生成する。各ワーカーは`worker`関数を実行するゴルーチンとして起動される
	// - 各ワーカーには一意のID(i)を与え、`jobChan`チャネルからタスクを受け取って処理し、その結果を`resultChan`チャネルに送信する
	// - ワーカーでエラーが発生した場合は、`errChan`チャネルに送信する
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for i := 1; i <= s.workerCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := worker(ctx, i, jobChan, resultChan); err != nil {
				cancel()
				errChan <- err
			}
		}(i)
	}

	// タスク割り当て
	// - `gaijiList`を基に`job`を作成し、`jobChan`チャネルに送信し、ワーカーに処理させる
	// - `gaijiList`の全てのデータが`jobChan`チャネルに送信された後、`close(jobChan)`によりチャネルをクローズする。
	// - これにより、追加のタスクがないことがワーカーに通知される
	go func() {
		defer close(jobChan)
		if err := createJobs(ctx, gaijiList, jobChan); err != nil {
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

func createJobs(baseSql string, gaijiList []*cmd.Gaiji, jobChan chan<- job) error {
	slog.Debug("[createJobs] START")
	i := 0
	defer func() {
		slog.Info(fmt.Sprintf("[createJobs] END : 生成したジョブの数=%d", i))
	}()

	// s.sqlとgaijiListを基に抽出用SQLを生成し、jobChanに送信する
	for _, g := range gaijiList {
		jobChan <- job{moji: g.Moji, codepoint: g.Codepoint, sql: fmt.Sprintf(baseSql, g.Moji)}
		i++
		slog.Debug(fmt.Sprintf("[createJobs] add : job %d", i))
	}

	return nil
}
