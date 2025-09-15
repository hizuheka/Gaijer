package search

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"Gaijer/cmd"

	"github.com/google/subcommands"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/sync/errgroup"
)

// Job はワーカーが処理するタスクを表します。
// Bytesフィールドを追加して、処理済みバイト数に基づいた進捗更新を可能にします。
type Job struct {
	LineNumber int
	Text       string
	// Bytes      int // バイト数フィールドを追加
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

// fileInfo は処理対象ファイルの情報を保持します。
type fileInfo struct {
	path string
	size int64
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

// Execute はワーカープールパターンで並列処理を実行します。
func (c *SearchCmd) Execute(_ context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	// --- ステップ1: 現在のslog設定を保存し、deferで復元を予約 ---
	originalLogger := slog.Default()
	defer slog.SetDefault(originalLogger)

	// --- ステップ2: 新しいロガーを、現在のレベルを引き継いで作成 ---
	p := mpb.New(mpb.WithAutoRefresh())
	// 現在のログレベルを検知
	currentLevel := getCurrentSlogLevel()
	// mpbコンテナ(p)を出力先とし、検知したレベルで新しいハンドラを作成
	handler := slog.NewTextHandler(p, &slog.HandlerOptions{Level: currentLevel})
	// 新しいロガーを作成し、デフォルトに設定（この関数内でのみ有効）
	slog.SetDefault(slog.New(handler))

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
		return subcommands.ExitUsageError
	}

	gaijiMap, err := c.createGaijiMap()
	if err != nil {
		slog.Error("外字マップの作成に失敗しました。", "error", err)
		hadError = true
		return subcommands.ExitFailure
	}

	filesToProcess, totalSize, err := c.gatherFiles(c.inputFolder)
	if err != nil {
		slog.Error("ファイル情報の収集に失敗しました。", "error", err)
		hadError = true
		return subcommands.ExitFailure
	}

	if err := os.MkdirAll(c.outputFolder, 0755); err != nil {
		slog.Error("出力フォルダの作成に失敗しました。", "folder", c.outputFolder, "error", err)
		hadError = true
		return subcommands.ExitFailure
	}

	// 全体進捗バーの作成
	// p := mpb.New(mpb.WithAutoRefresh())
	var processedFiles atomic.Int64
	totalFiles := len(filesToProcess)

	fileCounterDecorator := decor.Any(func(statistics decor.Statistics) string {
		count := processedFiles.Load()
		return fmt.Sprintf("(Files: %d/%d)", count, totalFiles)
	}, decor.WCSyncWidth)

	// overallBar := p.New(totalSize,
	// mpb.BarStyle().Lbound("╢").Filler("█").Tip("█").Padding("░").Rbound("╟"),
	overallBar := p.AddBar(totalSize,
		mpb.PrependDecorators(
			decor.Name("全体", decor.WCSyncWidth),
			decor.CountersKibiByte(" % .2f / % .2f "),
		),
		mpb.AppendDecorators(
			decor.Percentage(decor.WCSyncSpace),
			fileCounterDecorator,
		),
	)

	var wg sync.WaitGroup
	jobs := make(chan fileInfo, len(filesToProcess))

	barOptionsBase :=
		mpb.AppendDecorators(
			decor.OnComplete(decor.Percentage(decor.WC{W: 3}), "✓"),
		)
	// style := mpb.BarStyle().Lbound("╢").Filler("█").Tip("█").Padding("░").Rbound("╟")

	for i := 0; i < c.workerCount; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			for currentFile := range jobs {
				// 0バイトのファイルは処理をスキップする
				if currentFile.size == 0 {
					slog.Info("0バイトのファイルをスキップします。", "file", currentFile.path)
					// 全体進捗だけを更新して、このファイルの処理を完了とする
					overallBar.IncrInt64(0) // 念のため
					processedFiles.Add(1)
					continue // 次のジョブへ
				}
				// 新しいファイル処理のたびに、新しいバーを動的に作成する
				var barOptions []mpb.BarOption
				barOptions = append(barOptions,
					mpb.PrependDecorators(
						decor.Name(fmt.Sprintf("W%d:", i+1), decor.WCSyncWidth),
						decor.Name(currentFile.path, decor.WCSyncWidth),
					))
				barOptions = append(barOptions, mpb.BarRemoveOnComplete())
				barOptions = append(barOptions, barOptionsBase)
				// 以前のバーがあれば、その後ろにキューイングする
				bar := p.AddBar(currentFile.size, barOptions...)
				// workerbar[workerID] = p.New(currentFile.size, style, barOptions...)

				inputFile := filepath.Join(c.inputFolder, currentFile.path)
				outputFile := filepath.Join(c.outputFolder, currentFile.path+".out")
				if err := c.processFile(inputFile, outputFile, gaijiMap, bar); err != nil {
					slog.Error("ファイルの処理に失敗しました。", "file", inputFile, "error", err)
					hadError = true
					bar.Abort(false)
				}

				// 正常完了の場合、barは自動的に100%になりCompleted状態になる
				overallBar.IncrInt64(currentFile.size)
				processedFiles.Add(1)
			}
		}(i) // ループ変数 i をキャプチャさせない
	}

	for _, file := range filesToProcess {
		jobs <- file
	}
	close(jobs)

	wg.Wait()

	p.Wait()

	if hadError {
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

// gatherFiles は処理対象のファイル情報と合計サイズを収集するヘルパー関数
func (c *SearchCmd) gatherFiles(inputFolder string) ([]fileInfo, int64, error) {
	entries, err := os.ReadDir(inputFolder)
	if err != nil {
		return nil, 0, fmt.Errorf("入力フォルダの読み取りに失敗: %w", err)
	}

	var filesToProcess []fileInfo
	var totalSize int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			slog.Warn("ファイル情報の取得に失敗、スキップします。", "file", entry.Name(), "error", err)
			continue
		}
		filesToProcess = append(filesToProcess, fileInfo{path: entry.Name(), size: info.Size()})
		totalSize += info.Size()
	}
	return filesToProcess, totalSize, nil
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

// processFile は単一ファイルに対する処理フロー。
func (c *SearchCmd) processFile(inputFile, outputFile string, gaijiMap map[rune]*cmd.Gaiji, bar *mpb.Bar) error {
	file, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("ファイルのオープンに失敗: %w", err)
	}
	defer file.Close()

	proxyReader := bar.ProxyReader(file)
	defer proxyReader.Close()

	// results, err := c.runPipeline(proxyReader, gaijiMap, bar)
	results, err := c.runPipeline(proxyReader, gaijiMap)
	if err != nil {
		// bar.Abort(false) はProxyReaderがCloseされるときに自動で処理されるので不要
		return fmt.Errorf("処理パイプラインでエラーが発生しました: %w", err)
	}

	// 結果をソートします。
	sortResults(results)

	if err := cmd.WriteOutputFile(outputFile, results, c.header, c.value); err != nil {
		return fmt.Errorf("結果の出力に失敗しました: %w", err)
	}

	return nil
}

// runPipeline はワーカープールをセットアップし、ファイル処理を実行します。
// func (c *SearchCmd) runPipeline(reader io.Reader, gaijiMap map[rune]*cmd.Gaiji, bar *mpb.Bar) ([]cmd.Result, error) {
func (c *SearchCmd) runPipeline(reader io.Reader, gaijiMap map[rune]*cmd.Gaiji) ([]cmd.Result, error) {
	g, ctx := errgroup.WithContext(context.Background())

	jobChan := make(chan Job, c.workerCount*jobChanBufferMultiplier)
	resultChan := make(chan cmd.Result, c.workerCount*resultChanBufferMultiplier)

	// ワーカーゴルーチンを起動
	for i := 1; i <= c.workerCount; i++ {
		id := i // ループ変数のキャプチャ問題を防ぐ
		g.Go(func() error {
			// worker(ctx, id, jobChan, resultChan, gaijiMap, bar)
			worker(ctx, id, jobChan, resultChan, gaijiMap)
			return nil // workerはエラーを返さない設計のため
		})
	}

	g.Go(func() error {
		defer close(jobChan)
		// createJobsから返されたエラーはerrgroupによって捕捉される
		return createJobs(ctx, reader, jobChan)
	})

	// すべてのワーカーが終了したらresultChanを閉じるためのゴルーチン
	go func() {
		g.Wait() // ジョブ生成と全ワーカーの終了を待つ
		close(resultChan)
	}()

	results := cmd.CollectResults(resultChan)

	// すべてのゴルーチンが終了するのを待ち、最初のエラーを受け取る
	if err := g.Wait(); err != nil {
		return nil, err
	}

	return results, nil
}

// sortResults は検索結果をコードポイントと行番号でソートします。
func sortResults(results []cmd.Result) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Codepoint == results[j].Codepoint {
			return results[i].Id < results[j].Id
		}
		return results[i].Codepoint < results[j].Codepoint
	})
}

// createJobs は入力ファイルを1行ずつ読み込み、ジョブチャネルにタスクを送信します。
func createJobs(ctx context.Context, reader io.Reader, jobChan chan<- Job) error {
	slog.Debug("ジョブの生成を開始します。", "section", "createJobs")
	lineNumber := 0
	// r := bufio.NewReader(reader)
	scanner := bufio.NewScanner(reader)

	// for {
	for scanner.Scan() {
		lineNumber++
		// // '\n' を区切り文字として、改行コードを含む文字列を読み取る
		// line, err := r.ReadString('\n')
		// lineBytes := len([]byte(line)) + 1 // +1は \n の分
		line := scanner.Text()
		// lineText := string(lineBytes) // 文字列が必要な場合は変換する

		// 読み込んだデータがある場合は、エラーが発生していてもジョブとして処理する
		// (ファイルの最終行に改行がない場合など)
		// if len(line) > 0 {
		// 	// ReadStringは文字列を返すため、コピーは不要
		// 	select {
		// 	case <-ctx.Done():
		// 		slog.Warn("ジョブ生成がキャンセルされました。", "section", "createJobs")
		// 		return ctx.Err()
		// 	case jobChan <- Job{LineNumber: lineNumber, Text: line, Bytes: lineBytes}:
		// 		// len(line)には改行コードのバイト数も含まれるため、正確な進捗更新が可能
		// 	}
		// }
		// // エラーハンドリング
		// if err != nil {
		// 	// ファイルの終端に到達したら、正常にループを抜ける
		// 	if err == io.EOF {
		// 		break
		// 	}
		// 	// その他のエラーの場合は、エラーを返す
		// 	return fmt.Errorf("ファイルの読み込み中にエラーが発生しました: %w", err)
		// }
		select {
		case <-ctx.Done():
			slog.Warn("ジョブ生成がキャンセルされました。", "section", "createJobs")
			return ctx.Err()
		case jobChan <- Job{LineNumber: lineNumber, Text: line}:
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("ファイルの読み込み中にエラーが発生しました: %w", err)
	}

	slog.Debug("ジョブの生成が完了しました。", "section", "createJobs", "total_jobs", lineNumber)

	return nil
}

// worker はジョブチャネルからタスクを受け取り、外字検索を実行して結果を送信します。
// func worker(ctx context.Context, id int, jobs <-chan Job, results chan<- cmd.Result, gaijiMap map[rune]*cmd.Gaiji, bar *mpb.Bar) {
func worker(ctx context.Context, id int, jobs <-chan Job, results chan<- cmd.Result, gaijiMap map[rune]*cmd.Gaiji) {
	slog.Debug("ワーカーを開始します。", "section", "worker", "id", id)
	defer slog.Debug("ワーカーを終了します。", "section", "worker", "id", id)

	j := 0
	for job := range jobs {
		j++
		slog.Debug("processing job", "section", "worker", "id", id, "processing index", j)
		select {
		case <-ctx.Done():
			slog.Warn("ワーカー処理がキャンセルされました。", "section", "worker", "id", id)
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
							Id:        fmt.Sprintf("%08d", job.LineNumber),
							Value:     strings.Trim(strings.ReplaceAll(job.Text, "\r\n", ""), `"`),
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
			// bar.IncrBy(job.Bytes)
		}
	}
}

// getCurrentSlogLevel は、現在のデフォルトロガーの有効なログレベルを判定して返します。
func getCurrentSlogLevel() slog.Level {
	ctx := context.Background()
	// 詳細なレベルから順にチェック
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		return slog.LevelDebug
	}
	if slog.Default().Enabled(ctx, slog.LevelInfo) {
		return slog.LevelInfo
	}
	if slog.Default().Enabled(ctx, slog.LevelWarn) {
		return slog.LevelWarn
	}
	return slog.LevelError
}
