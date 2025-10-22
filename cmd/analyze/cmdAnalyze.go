package analyze

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/google/subcommands"
)

// AnalyzeCmd は 'analyze' コマンドの構造を定義します。
type AnalyzeCmd struct {
	inputFile  string
	outputFile string
}

// 型エイリアスでコードの意図を明確化
type (
	AtenaNo  string
	CharCode string
	CharSet  map[rune]struct{}
)

// AnalyzeRecord は分析対象CSVの1行のデータを保持します
type AnalyzeRecord struct {
	CharCode CharCode
	Char     rune
	AtenaNo  AtenaNo
}

func (*AnalyzeCmd) Name() string { return "analyze" }
func (*AnalyzeCmd) Synopsis() string {
	return "外字の使用状況を分析し、最適な宛名番号の組み合わせを抽出します。"
}
func (*AnalyzeCmd) Usage() string {
	return `analyze -i <分析対象ファイル> -o <出力ファイル>:
	CSVファイルを分析し、全ての文字をカバーするための宛名番号の組み合わせを計算して出力します。
`
}

func (p *AnalyzeCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.inputFile, "i", "", "分析対象のCSVファイルパス")
	f.StringVar(&p.outputFile, "o", "", "分析結果の出力ファイルパス")
}

// validate はコマンドライン引数が正しく設定されているか検証します。
func (p *AnalyzeCmd) validate() error {
	if p.inputFile == "" {
		return fmt.Errorf("引数 -i (分析対象ファイル) が指定されていません")
	}
	if p.outputFile == "" {
		return fmt.Errorf("引数 -o (出力ファイル) が指定されていません")
	}
	if _, err := os.Stat(p.inputFile); os.IsNotExist(err) {
		return fmt.Errorf("分析対象ファイルが存在しません: %s", p.inputFile)
	}
	return nil
}

// Execute はコマンドのメインロジックを実行します。
func (c *AnalyzeCmd) Execute(ctx context.Context, f *flag.FlagSet, _ ...any) subcommands.ExitStatus {
	slog.Info("analyzeコマンドを開始します。")

	if err := c.validate(); err != nil {
		slog.Error("引数の検証に失敗しました。", "error", err)
		return subcommands.ExitUsageError
	}

	// ステップ1：準備段階（データ構造の構築）
	records, err := readAnalyzeCSV(c.inputFile)
	if err != nil {
		slog.Error("ファイルの読み込みに失敗しました。", "file", c.inputFile, "error", err)
		return subcommands.ExitFailure
	}
	slog.Info("分析対象ファイルを読み込みました。", "行数", len(records))

	// ステップ2：分析ループ（貪欲法の実行）
	result, charToCode, err := analyzeRecords(ctx, records)
	if err != nil {
		if err == context.Canceled {
			slog.Warn("処理がユーザーによってキャンセルされました。")
			return subcommands.ExitFailure
		}
		slog.Error("分析処理中に予期せぬエラーが発生しました。", "error", err)
		return subcommands.ExitFailure
	}
	slog.Info("分析処理が完了しました。")

	// ステップ3：結果のまとめと統計情報
	if err := writeAnalyzeResult(c.outputFile, result, charToCode); err != nil {
		slog.Error("結果の書き込みに失敗しました。", "file", c.outputFile, "error", err)
		return subcommands.ExitFailure
	}

	slog.Info("全ての処理が正常に完了しました。", "出力ファイル", c.outputFile)
	return subcommands.ExitSuccess
}

// atenaInfo は各宛名番号の情報を保持する構造体です。
type atenaInfo struct {
	chars CharSet // この宛名番号が持つ文字のセット
}

// analyzeRecords は、Recordのスライスを受け取り、分析のコアロジックを実行します。
func analyzeRecords(ctx context.Context, records []AnalyzeRecord) (result map[rune]AtenaNo, charToCode map[rune]CharCode, err error) {
	if len(records) == 0 {
		slog.Warn("分析対象のレコードが0件です。")
		return make(map[rune]AtenaNo), make(map[rune]CharCode), nil
	}

	// 各「宛名番号」が、どんな文字を持っているか（chars）、そして現時点でいくつの「未発見の文字」を発見できるか（coverCount）を記録するリストです。
	atenaData := make(map[AtenaNo]*atenaInfo) // 宛名番号からその情報へのマッピング
	// 分析開始時点での、全てのユニークな文字のリストです。分析が進むにつれて、このリストから文字が消えていきます。
	uncoveredChars := make(CharSet)      // まだカバーされていない文字のセット
	charToCode = make(map[rune]CharCode) // 文字から文字コードへのマッピング

	for _, rec := range records {
		// atenaDataへの追加(初回のみ)
		if _, ok := atenaData[rec.AtenaNo]; !ok {
			atenaData[rec.AtenaNo] = &atenaInfo{
				chars: make(CharSet),
			}
		}
		atenaData[rec.AtenaNo].chars[rec.Char] = struct{}{}
		// uncoveredChars(まだカバーされていない文字のセット)への追加(初回のみ)
		uncoveredChars[rec.Char] = struct{}{}

		// 同じ文字に別の文字コードが割り当てられている場合、警告を出すが最初の値を採用する
		if existingCode, ok := charToCode[rec.Char]; ok {
			if existingCode != rec.CharCode {
				slog.Warn("同じ文字に異なる文字コードが割り当てられています。最初の値を採用します。",
					"文字", rec.Char, "登録済み文字コード", existingCode, "新しい文字コード", rec.CharCode)
			}
		} else {
			charToCode[rec.Char] = rec.CharCode
		}
	}
	slog.Info("データ構造の構築が完了しました。", "記録した宛名番号", len(atenaData), "記録した文字数", len(uncoveredChars))

	// もしuncoveredCharsが空なら、すぐに終了する
	if len(uncoveredChars) == 0 {
		slog.Warn("分析対象となる有効な文字が見つかりませんでした。")
		return make(map[rune]AtenaNo), charToCode, nil
	}

	slog.Info("貪欲法による宛名番号の選択を開始します...")
	result = make(map[rune]AtenaNo)
	totalChars := len(uncoveredChars)

	// 貪欲法のループ
	for len(uncoveredChars) > 0 {
		// コンテキストのキャンセルをチェック
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}

		var bestAtena AtenaNo
		maxCoverCount := -1

		// 毎回、現在の未カバー文字を最も多くカバーできる宛名番号を数え直す
		for atena, info := range atenaData {
			currentCover := 0
			for char := range info.chars {
				if _, ok := uncoveredChars[char]; ok {
					currentCover++
				}
			}
			if currentCover > maxCoverCount {
				maxCoverCount = currentCover
				bestAtena = atena
			}
		}

		// 全ての文字がカバーされていれば、maxCoverCountは0になり、ループを正常に抜ける
		if maxCoverCount <= 0 {
			break
		}

		// 見つけ出したbestAtenaが持っている文字を全て確認します。
		charsToCover := atenaData[bestAtena].chars // bestAtenaがカバーする文字のセット

		newlyCovered := 0
		for char := range charsToCover {
			// その中に「未発見」の文字があれば、それを「発見済み」とし、結果リスト（result）に「この文字はbestAtenaで見つけました」と記録します。
			// 発見した文字を「未発見の文字リスト (uncoveredChars)」から削除します。
			if _, ok := uncoveredChars[char]; ok {
				result[char] = bestAtena
				delete(uncoveredChars, char)
				newlyCovered++
			}
		}

		// 進捗状況の出力
		progress := 100 * (1 - float64(len(uncoveredChars))/float64(totalChars))
		slog.Info("処理中...", "進捗率", fmt.Sprintf("%.2f%%", progress), "選択済み宛名番号", bestAtena, "発見済みとした文字数", newlyCovered, "残っている文字数", len(uncoveredChars))
	}

	if len(uncoveredChars) > 0 {
		var uncoveredList []string
		for char := range uncoveredChars {
			uncoveredList = append(uncoveredList, string(char))
			if len(uncoveredList) >= 10 {
				uncoveredList = append(uncoveredList, "...")
				break
			}
		}
		slog.Error("アルゴリズムが終了しましたが、未カバーの文字が残っています。入力データに問題がある可能性があります。",
			"uncovered_count", len(uncoveredChars),
			"examples", strings.Join(uncoveredList, ", "))
	}

	atenaUsed := make(map[AtenaNo]struct{})
	for _, atena := range result {
		atenaUsed[atena] = struct{}{}
	}
	slog.Info("分析統計", "発見した文字数", len(result), "抽出した宛名数", len(atenaUsed), "元の宛名数", len(atenaData))

	// 完成したresultマップ（どの文字をどの宛名番号でカバーしたか）と、charToCodeマップ（文字と文字コードの対応表）を返します。
	return result, charToCode, nil
}

// readAnalyzeCSV はCSVファイルを読み込み、AnalyzeRecordのスライスとして返します
func readAnalyzeCSV(filePath string) ([]AnalyzeRecord, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	var records []AnalyzeRecord
	lineNum := 0
	isFirstLine := true

	for {
		lineNum++
		line, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%d行目の読み込みエラー: %w", lineNum, err)
		}

		if isFirstLine {
			isFirstLine = false
			if len(line) > 0 && (line[0] == "コード") {
				slog.Info("ヘッダー行をスキップしました。")
				continue
			}
		}

		if len(line) < 4 {
			slog.Warn("4列未満の不正なレコードをスキップします。", "line", lineNum, "record", line)
			continue
		}
		if line[0] == "" || line[1] == "" || line[3] == "" {
			slog.Warn("空の値を含むレコードをスキップします。", "line", lineNum, "record", line)
			continue
		}

		records = append(records, AnalyzeRecord{
			CharCode: CharCode(line[0]),
			Char:     []rune(line[1])[0],
			AtenaNo:  extractAtenaNo(line[2])})
	}
	return records, nil
}

// extractAtenaNoは "文字列@宛名番号" の形式から宛名番号を抽出します。
func extractAtenaNo(input string) AtenaNo {
	trimmed := strings.TrimSpace(input)
	// @が含まれているか確認し、含まれていれば@で分割して後半部分を返す
	if lastIndex := strings.LastIndex(trimmed, "@"); lastIndex != -1 {
		return AtenaNo(trimmed[lastIndex+1:])
	}
	// @が含まれていない場合は、全体を宛名番号として返す
	return AtenaNo(trimmed)
}

// writeAnalyzeResult は分析結果を指定されたファイルにCSV形式で書き出します
func writeAnalyzeResult(filePath string, result map[rune]AtenaNo, charToCode map[rune]CharCode) error {
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("出力ファイルを作成できませんでした: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)

	if err := writer.Write([]string{"コード", "文字", "宛名番号"}); err != nil {
		return fmt.Errorf("ヘッダーの書き込みに失敗しました: %w", err)
	}

	var chars []rune
	for char := range result {
		chars = append(chars, char)
	}
	sort.Slice(chars, func(i, j int) bool {
		return chars[i] < chars[j]
	})

	for _, char := range chars {
		atenaNo := result[char]
		charCode := charToCode[char]
		if err := writer.Write([]string{string(charCode), string(char), string(atenaNo)}); err != nil {
			return fmt.Errorf("レコード '%s' の書き込みに失敗しました: %w", string(char), err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("CSVの書き込みフラッシュに失敗しました: %w", err)
	}

	return nil
}
