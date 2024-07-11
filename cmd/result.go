package cmd

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"os"
)

type Result struct {
	Moji      rune
	Codepoint string
	Id        string
	Attr      string
	Value     string
}

func (r *Result) Csv(isOutputValue bool) []string {
	if isOutputValue {
		return []string{r.Codepoint, string(r.Moji), r.Id, r.Attr, r.Value}
	} else {
		return []string{r.Codepoint, string(r.Moji), r.Id, r.Attr}
	}
}

func ResultHeaderAry() []string {
	return []string{"コード", "文字", "識別番号", "属性", "値"}
}

// 出力ファイルに書き出す
func WriteOutputFile(outputFilePath string, results []Result, isOutputHeader bool, isOutputValue bool) error {
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
		if err := writer.Write(ResultHeaderAry()); err != nil {
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

func CollectResults(resultChan <-chan Result) []Result {
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
