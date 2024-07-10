package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/google/subcommands"
)

type sqlCmd struct {
	table       string
	sel         string
	where       string
	orderby     string
	output      string
	gaiji       string
	workerCount int
	header      bool
	value       bool
}

func (*sqlCmd) Name() string { return "sql" }
func (*sqlCmd) Synopsis() string {
	return "[table]テーブルから、[gaiji]ファイルと[select]と[where]により生成したSQLを基に抽出した結果を、[output]ファイルに出力する"
}
func (*sqlCmd) Usage() string {
	return `sql -table 検索対象テーブル -select SELECT句 -where WHERE句 [-orderby ORDERBY句] -o 検索結果ファイル -g 外字リストファイル [-w 並行処理数] [-header] [-value]:
	検索対象テーブルから、外字リストファイルとSELECT句、WHERE句、ORDERBY句から生成したSQLを基に抽出した結果を、検索結果ファイルに出力する
`
}

func (s *sqlCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&s.table, "table", "", "検索対象テーブル")
	f.StringVar(&s.sel, "select", "", "SELECT句")
	f.StringVar(&s.where, "where", "", "WHERE句")
	f.StringVar(&s.orderby, "orderby", "", "ORDERBY句")
	f.StringVar(&s.output, "o", "", "検索結果ファイルのパス")
	f.StringVar(&s.gaiji, "g", "", "外字リストファイルのパス")
	f.IntVar(&s.workerCount, "w", 1, "並行処理数")
	f.BoolVar(&s.header, "header", false, "ヘッダの出力有無")
	f.BoolVar(&s.value, "value", false, "値の出力有無")
}

func (s *sqlCmd) validate() error {
	if s.table == "" {
		return fmt.Errorf("引数 -table が指定されていません。")
	}
	if s.sel == "" {
		return fmt.Errorf("引数 -select が指定されていません。")
	}
	if s.where == "" {
		return fmt.Errorf("引数 -where が指定されていません。")
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

func (s *sqlCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
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
	var gaijiList []*gaiji
	gaijiList, err = createGaijiList(p.gaiji)
	if err != nil {
		return subcommands.ExitFailure
	}

	// タスク準備
	// - ジョブキューを管理するチャネル (`jobChan`)を準備する。バッファは適当・・・
	// - 結果を格納するチャネル(`resultChan`)を準備する。バッファは適当・・・
	// - 発生したエラーを確認するためのチャネル(`errChan`)を準備する
	jobChan := make(chan string, p.workerCount*100)
	resultChan := make(chan Result, p.workerCount*10)
	errChan := make(chan error, p.workerCount)

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
	// - `p.input`ファイルを読み込み、`jobChan`チャネルに送信し、ワーカーに処理させる
	// - `p.input`の全ての行が`jobChan`チャネルに送信された後、`close(jobChan)`によりチャネルをクローズする。
	// - これにより、追加のタスクがないことがワーカーに通知される
	go func() {
		defer close(jobChan)
		if err := createJobs(ctx, s.input, jobChan); err != nil {
			cancel()
			errChan <- err
		}
	}()

	// 結果の集約
	// - `resultChan`チャネルがcloseするまで受信し、`results`に追加する
	// - `resultChan`チャネルからの受信後は、`close(done)`によりチャネルをクローズする
	// - これにより、結果の集約が完了したことを通知する
	done := make(chan struct{})
	var results []Result
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
		if results[i].codepoint == results[j].codepoint {
			return results[i].id < results[j].id
		}
		return results[i].codepoint < results[j].codepoint
	})

	// 結果の出力
	// - `result`の内容を`p.output`に出力する。
	err = writeOutputFile(p.output, results, p.header, p.value)
	if err != nil {
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}
