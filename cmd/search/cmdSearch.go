package search

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"

	"Gaijer/cmd"

	"github.com/google/subcommands"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/sync/errgroup"
)

// Job はワーカーが処理するタスクを表します。
type Job struct {
	LineNumber int
	Text       string
}

// SearchCmd は 'search' コマンドの構造を定義します。
type SearchCmd struct {
	inputFolder                   string
	outputFolder                  string
	gaijiFile                     string
	workerCount                   int
	innerWorkerCount              int
	header                        bool
	value                         bool
	sequentialProcessingThreshold int64 // 閾値を格納するフィールドを追加
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
	maxScanTokenSize           = 1 * 1024 * 1024 // 1MBに設定
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
	f.IntVar(&p.innerWorkerCount, "inner-w", 4, "ファイル内並行処理数 (デフォルト: 4)")
	f.BoolVar(&p.header, "header", false, "結果ファイルにヘッダを出力するかどうか")
	f.BoolVar(&p.value, "value", false, "結果ファイルに値（該当行のテキスト）を出力するかどうか")
	// -threshold フラグを追加
	f.Int64Var(&p.sequentialProcessingThreshold, "threshold", 1, "並列/逐次処理を切り替えるファイルサイズの閾値 (MB, デフォルト: 1MB)")
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
	if p.innerWorkerCount <= 0 {
		return fmt.Errorf("引数 -inner-w (ファイル内並行処理数) には1以上の整数を指定してください: %d", p.innerWorkerCount)
	}

	return nil
}

// Execute はワーカープールパターンで並列処理を実行します。
func (c *SearchCmd) Execute(ctx context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
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

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	p := mpb.New(mpb.WithAutoRefresh())

	// --- ステップ1: 現在のslog設定を保存し、deferで復元を予約 ---
	// 現在のslogを再設定する方法では期待どおりの動作しなかったのえ、slogを設定しなおしている
	currentLevel := getCurrentSlogLevel() // 現在のログレベルを検知
	// originalLogger := slog.Default()  // <- これが期待通りに動作しない。ログが出力されなくなってしまう
	defer func() {
		// slog.SetDefault(originalLogger)
		handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: currentLevel})
		slog.SetDefault(slog.New(handler))
	}()

	// --- ステップ2: 新しいロガーを、現在のレベルを引き継いで作成 ---
	// mpbコンテナ(p)を出力先とし、検知したレベルで新しいハンドラを作成
	handler := slog.NewTextHandler(p, &slog.HandlerOptions{Level: currentLevel})
	// 新しいロガーを作成し、デフォルトに設定（この関数内でのみ有効）
	slog.SetDefault(slog.New(handler))

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
	var processedFiles atomic.Int64
	totalFiles := len(filesToProcess)

	overallBar := p.AddBar(totalSize,
		mpb.PrependDecorators(
			decor.Name("全体", decor.WCSyncWidth),
			decor.CountersKibiByte(" % .2f / % .2f "),
		),
		mpb.AppendDecorators(
			decor.Percentage(decor.WCSyncSpace),
			decor.Any(func(s decor.Statistics) string {
				return fmt.Sprintf("(Files: %d/%d)", processedFiles.Load(), totalFiles)
			}, decor.WCSyncWidth),
			// fileCounterDecorator,
		),
	)

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(c.workerCount)
	// var wg sync.WaitGroup
	jobs := make(chan fileInfo, len(filesToProcess))

	for _, file := range filesToProcess {
		jobs <- file
	}
	close(jobs)

	for i := 0; i < c.workerCount; i++ {
		g.Go(func() error {
			for {
				select {
				case <-gCtx.Done():
					return gCtx.Err()
				case currentFile, ok := <-jobs:
					if !ok {
						return nil
					}

					if currentFile.size == 0 {
						slog.Debug("0バイトのファイルを処理し、空の出力ファイルを作成します。", "file", currentFile.path)

						// 出力ファイルパスを作成
						outputFile := filepath.Join(c.outputFolder, currentFile.path+".out")

						// 空のファイルを作成
						file, err := os.Create(outputFile)
						if err != nil {
							slog.Error("空の出力ファイルの作成に失敗しました。", "file", outputFile, "error", err)
							hadError = true // エラーがあったことを記録
						} else {
							// 作成したらすぐに閉じる
							file.Close()
						}

						// 全体進捗を更新して、このファイルの処理を完了とする
						processedFiles.Add(1)
						// overallBarは0バイトなので進めない

						continue // 次のジョブへ
					}

					bar := p.AddBar(currentFile.size,
						mpb.BarRemoveOnComplete(),
						mpb.PrependDecorators(
							decor.Name(fmt.Sprintf("W%d:", i+1), decor.WCSyncWidth),
							decor.Name(currentFile.path, decor.WCSyncWidth),
						),
						mpb.AppendDecorators(
							decor.OnComplete(decor.Percentage(decor.WC{W: 3}), "✓"),
						),
					)

					inputFile := filepath.Join(c.inputFolder, currentFile.path)
					outputFile := filepath.Join(c.outputFolder, currentFile.path+".out")
					err := c.processFile(gCtx, inputFile, outputFile, gaijiMap, bar)

					if err != nil {
						// context canceled errorは正常終了の一部なのでログレベルを下げる
						if err == context.Canceled {
							slog.Warn("contextのキャンセルによりファイル処理が中断されました。", "file", inputFile)
						} else {
							slog.Error("ファイルの処理に失敗しました。", "file", inputFile, "error", err)
							hadError = true
						}
						bar.Abort(false)
						// エラーが発生しても他のファイルの処理を続けるため、errgroupにはエラーを返さない
					}

					overallBar.IncrInt64(currentFile.size)
					processedFiles.Add(1)
				}
			}
		})
	}
	if err := g.Wait(); err != nil {
		hadError = true
	}
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
// processFile はファイルサイズに基づき、逐次処理と並行処理を切り替える司令塔として機能します。
func (c *SearchCmd) processFile(ctx context.Context, inputFile, outputFile string, gaijiMap map[rune]*cmd.Gaiji, bar *mpb.Bar) error {
	stat, err := os.Stat(inputFile)
	if err != nil {
		return fmt.Errorf("ファイル情報の取得に失敗: %w", err)
	}
	if stat.Size() < c.sequentialProcessingThreshold*1024*1024 {
		slog.Debug("ファイルサイズが小さいため、逐次処理を開始します。", "file", inputFile)
		return c.processFileSequentially(ctx, inputFile, outputFile, gaijiMap, bar)
	}
	slog.Debug("ファイルサイズが大きいため、並行処理を開始します。", "file", inputFile)
	return c.processFileInParallel(ctx, inputFile, outputFile, gaijiMap, bar)
}

// processFileInParallel は、巨大なファイルに対して並行パイプライン処理を実行します。
func (c *SearchCmd) processFileInParallel(ctx context.Context, inputFile, outputFile string, gaijiMap map[rune]*cmd.Gaiji, bar *mpb.Bar) error {
	file, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("ファイルのオープンに失敗: %w", err)
	}
	defer file.Close()
	proxyReader := bar.ProxyReader(file)
	defer proxyReader.Close()
	results, err := c.runPipeline(ctx, proxyReader, gaijiMap)
	if err != nil {
		return fmt.Errorf("処理パイプラインでエラーが発生しました: %w", err)
	}
	sortResults(results)
	if err := cmd.WriteOutputFile(outputFile, results, c.header, c.value); err != nil {
		return fmt.Errorf("結果の出力に失敗しました: %w", err)
	}
	return nil
}

// processFileSequentially は、小さなファイルに対して単純な逐次処理を実行します。
func (c *SearchCmd) processFileSequentially(ctx context.Context, inputFile, outputFile string, gaijiMap map[rune]*cmd.Gaiji, bar *mpb.Bar) error {
	file, err := os.Open(inputFile)
	if err != nil {
		return fmt.Errorf("ファイルのオープンに失敗: %w", err)
	}
	defer file.Close()
	var allResults []cmd.Result
	proxyReader := bar.ProxyReader(file)
	defer proxyReader.Close()
	scanner := bufio.NewScanner(proxyReader)
	// スキャナのバッファサイズを増やす
	buf := make([]byte, maxScanTokenSize)
	scanner.Buffer(buf, maxScanTokenSize)

	lineNumber := 0
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			lineNumber++
			lineResults := searchLine(scanner.Text(), lineNumber, gaijiMap)
			if len(lineResults) > 0 {
				allResults = append(allResults, lineResults...)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("ファイルの読み込み中にエラーが発生しました: %w", err)
	}
	sortResults(allResults)
	if err := cmd.WriteOutputFile(outputFile, allResults, c.header, c.value); err != nil {
		return fmt.Errorf("結果の出力に失敗しました: %w", err)
	}
	return nil
}

// runPipeline はワーカープールをセットアップし、ファイル処理を実行します。
// func (c *SearchCmd) runPipeline(reader io.Reader, gaijiMap map[rune]*cmd.Gaiji, bar *mpb.Bar) ([]cmd.Result, error) {
func (c *SearchCmd) runPipeline(ctx context.Context, reader io.Reader, gaijiMap map[rune]*cmd.Gaiji) ([]cmd.Result, error) {
	g, ctx := errgroup.WithContext(context.Background())

	jobChan := make(chan Job, c.innerWorkerCount*jobChanBufferMultiplier)
	resultChan := make(chan cmd.Result, c.innerWorkerCount*resultChanBufferMultiplier)

	g.Go(func() error {
		defer close(jobChan)
		return createJobs(ctx, reader, jobChan)
	})
	for i := 0; i < c.innerWorkerCount; i++ {
		g.Go(func() error {
			return worker(ctx, i, jobChan, resultChan, gaijiMap)
		})
	}
	go func() {
		g.Wait()
		close(resultChan)
	}()
	results := cmd.CollectResults(resultChan)
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
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, maxScanTokenSize)
	scanner.Buffer(buf, maxScanTokenSize)

	for scanner.Scan() {
		lineNumber++
		select {
		case <-ctx.Done():
			slog.Warn("ジョブ生成がキャンセルされました。", "section", "createJobs")
			return ctx.Err()
		case jobChan <- Job{LineNumber: lineNumber, Text: scanner.Text()}:
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
func worker(ctx context.Context, id int, jobs <-chan Job, results chan<- cmd.Result, gaijiMap map[rune]*cmd.Gaiji) error {
	slog.Debug("ワーカーを開始します。", "section", "worker", "id", id)
	defer slog.Debug("ワーカーを終了します。", "section", "worker", "id", id)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case job, ok := <-jobs:
			if !ok {
				return nil
			}
			lineResults := searchLine(job.Text, job.LineNumber, gaijiMap)
			for _, result := range lineResults {
				select {
				case results <- result:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

// searchLine は単一の行を検索し、見つかった外字の結果スライスを返します。
// バイナリデータが含まれている可能性のある行を安全に処理します。
func searchLine(lineText string, lineNumber int, gaijiMap map[rune]*cmd.Gaiji) []cmd.Result {
	// バイナリデータを無害化（サニタイズ）する
	// 不正なUTF-8シーケンスを置換文字に置き換え、NULLバイトを完全に除去する
	sanitizedText := strings.ToValidUTF8(lineText, string(rune(0xFFFD)))
	sanitizedText = strings.ReplaceAll(sanitizedText, "\x00", "")

	var results []cmd.Result
	foundGaijiInLine := make(map[rune]struct{})

	for _, char := range sanitizedText { // 無害化されたテキストを検索する
		if g, ok := gaijiMap[char]; ok {
			if _, found := foundGaijiInLine[char]; !found {
				result := cmd.Result{
					Moji:      g.Moji,
					Codepoint: g.Codepoint,
					Id:        fmt.Sprintf("%08d", lineNumber),
					Value:     strings.Trim(strings.ReplaceAll(lineText, "\r\n", ""), `"`),
				}
				results = append(results, result)
				foundGaijiInLine[char] = struct{}{}
			}
		}
	}
	return results
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
