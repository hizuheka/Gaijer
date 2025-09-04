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
	"strings"
	"sync"

	"Gaijer/cmd"

	"github.com/google/subcommands"
)

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

func (p *SearchCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	var err error
	defer func() {
		if err != nil {
			slog.Error(err.Error())
		}
		slog.Info("END search-Command")
	}()

	slog.Info("START search-Command")

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

	// 入力フォルダ内のファイルをすべて取得
	files, err := os.ReadDir(p.inputFolder)
	if err != nil {
		slog.Error(fmt.Sprintf("入力フォルダの読み取りに失敗しました: %v", err))
		return subcommands.ExitFailure
	}

	// 出力フォルダが存在しない場合は作成
	if err := os.MkdirAll(p.outputFolder, os.ModePerm); err != nil {
		slog.Error(fmt.Sprintf("出力フォルダの作成に失敗しました: %v", err))
		return subcommands.ExitFailure
	}

	// 取得したファイルごとに処理を実行
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		inputFile := filepath.Join(p.inputFolder, file.Name())
		outputFile := filepath.Join(p.outputFolder, file.Name()+".out")
		slog.Info(fmt.Sprintf("処理開始: %s", inputFile))

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
			if err := createJobs(ctx, inputFile, jobChan); err != nil {
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
			results = cmd.CollectResults(resultChan)
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
		err = cmd.CheckError(errChan)

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
		slog.Info(fmt.Sprintf("[Execute] END sort.Slice : 抽出結果=%d", len(results)))

		// 結果の出力
		// - `result`の内容を`p.output`に出力する。
		err = cmd.WriteOutputFile(outputFile, results, p.header, p.value)

		if err != nil {
			return subcommands.ExitFailure
		}

		slog.Info(fmt.Sprintf("処理完了: %s -> %s", inputFile, outputFile))

		cancel() // 次のファイル処理のためにコンテキストをリセット
	}

	return subcommands.ExitSuccess
}

func createJobs(ctx context.Context, inputFile string, jobChan chan<- string) error {
	slog.Debug("[createJobs] START")
	i := 0
	defer func() {
		fmt.Println()
		slog.Info(fmt.Sprintf("[createJobs] END : 生成したジョブの数=%d", i))
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
		select {
		case <-ctx.Done():
			slog.Debug("[createJobs] canceled")
			return nil
		default:
			jobChan <- scanner.Text()
			readsize = readsize + int64(len(scanner.Bytes())) + 1 // +1 は改行コード分
			pr := int((float64(readsize) / float64(filesize)) * 100)
			// 進捗率(整数)が変化した場合のみ、コンソールに表示
			if progressRate != pr {
				progressRate = pr
				fmt.Fprintf(os.Stderr, "\r入力ファイル読込状況： %d %%", progressRate)
			}
			i++
			slog.Debug(fmt.Sprintf("[createJobs] add : job %d", i))
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
					results <- cmd.Result{
						Moji:      g.Moji,
						Codepoint: g.Codepoint,
						Id:        strings.Trim(a[0], "\""),
						Attr:      strings.Trim(a[1], "\""),
						Value:     strings.Trim(a[2], "\""),
					}
				}
			}
		}
	}

	return nil
}
