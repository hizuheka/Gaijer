package analyze

import (
	"context"
	"os"
	"reflect"
	"testing"
)

// --- analyzeRecords のテストケース ---

func TestAnalyzeRecords_EmptyInput(t *testing.T) {
	result, charToCode, err := analyzeRecords(context.Background(), []AnalyzeRecord{})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(result) != 0 || len(charToCode) != 0 {
		t.Errorf("Expected empty results for empty input, got result=%v, charToCode=%v", result, charToCode)
	}
}

func TestAnalyzeRecords_BasicCase(t *testing.T) {
	records := []AnalyzeRecord{
		{CharCode: "C001", Char: 'A', AtenaNo: "A001"},
		{CharCode: "C002", Char: 'B', AtenaNo: "A001"},
		{CharCode: "C003", Char: 'C', AtenaNo: "A002"},
	}
	expectedResult := map[rune]AtenaNo{'A': "A001", 'B': "A001", 'C': "A002"}
	expectedCharToCode := map[rune]CharCode{'A': "C001", 'B': "C002", 'C': "C003"}

	result, charToCode, err := analyzeRecords(context.Background(), records)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !reflect.DeepEqual(result, expectedResult) {
		t.Errorf("Result mismatch:\n Got: %v\nWant: %v", result, expectedResult)
	}
	if !reflect.DeepEqual(charToCode, expectedCharToCode) {
		t.Errorf("CharToCode mismatch:\n Got: %v\nWant: %v", charToCode, expectedCharToCode)
	}
}

func TestAnalyzeRecords_GreedyChoice(t *testing.T) {
	// 修正後のアルゴリズムの動作を確認するケース
	records := []AnalyzeRecord{
		{CharCode: "C001", Char: 'A', AtenaNo: "Set1"},
		{CharCode: "C002", Char: 'B', AtenaNo: "Set1"},
		{CharCode: "C003", Char: 'C', AtenaNo: "Set1"},
		{CharCode: "C001", Char: 'A', AtenaNo: "Set2"},
		{CharCode: "C002", Char: 'B', AtenaNo: "Set2"},
		{CharCode: "C004", Char: 'D', AtenaNo: "Set2"},
	}
	// 期待される結果: Set1が最初に選ばれ(A,B,Cをカバー)、次にSet2がDをカバーする
	expectedResult := map[rune]AtenaNo{'A': "Set1", 'B': "Set1", 'C': "Set1", 'D': "Set2"}

	result, _, err := analyzeRecords(context.Background(), records)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !reflect.DeepEqual(result, expectedResult) {
		t.Errorf("Result mismatch:\n Got: %v\nWant: %v", result, expectedResult)
	}
}

func TestAnalyzeRecords_ContextCancel(t *testing.T) {
	records := []AnalyzeRecord{
		{CharCode: "C001", Char: 'A', AtenaNo: "A001"},
		{CharCode: "C002", Char: 'B', AtenaNo: "A001"},
		{CharCode: "C003", Char: 'C', AtenaNo: "A002"}, // 多数のレコードを模倣
		{CharCode: "C004", Char: 'D', AtenaNo: "A003"},
		{CharCode: "C005", Char: 'E', AtenaNo: "A004"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // analyzeRecordsを呼び出す前にキャンセル

	_, _, err := analyzeRecords(ctx, records)
	if err == nil {
		t.Errorf("Expected context canceled error, got nil")
	}
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

// --- extractAtenaNo のテストケース ---

func TestExtractAtenaNo(t *testing.T) {
	testCases := []struct {
		input    string
		expected AtenaNo
	}{
		{"12345", "12345"},
		{"  12345  ", "12345"},
		{"string@12345", "12345"},
		{" string @ 12345 ", " 12345"},
		{"string@with@multiple@12345", "12345"},
		{"string@", ""},      // @で終わる場合
		{"@12345", "12345"},  // @で始まる場合
		{"string", "string"}, // @がない場合
		{"", ""},             // 空文字列
		{" @ ", ""},          // @の前後にスペースのみ
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			got := extractAtenaNo(tc.input)
			if got != tc.expected {
				t.Errorf("extractAtenaNo(%q) = %q; want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// --- readAnalyzeCSV のテストケース ---

func createTestCSV(t *testing.T, content string) string {
	t.Helper()
	tmpfile, err := os.CreateTemp("", "testcsv_*.csv")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	if _, err := tmpfile.WriteString(content); err != nil {
		tmpfile.Close()
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}
	return tmpfile.Name()
}

func TestReadAnalyzeCSV_Normal(t *testing.T) {
	content := `C001,A,Item1,A001
C002,B,string@A002,Item2
C003,C,Item3, A003 ` // 末尾スペース
	filePath := createTestCSV(t, content)
	defer os.Remove(filePath)

	records, err := readAnalyzeCSV(filePath)
	if err != nil {
		t.Fatalf("readAnalyzeCSV failed: %v", err)
	}

	expected := []AnalyzeRecord{
		{CharCode: "C001", Char: 'A', AtenaNo: "Item1"},
		{CharCode: "C002", Char: 'B', AtenaNo: "A002"},
		{CharCode: "C003", Char: 'C', AtenaNo: "Item3"},
	}
	if !reflect.DeepEqual(records, expected) {
		t.Errorf("Records mismatch:\n Got: %+v\nWant: %+v", records, expected)
	}
}

func TestReadAnalyzeCSV_WithHeader(t *testing.T) {
	content := `コード,文字,使用項目,宛名番号
C001,A,Item1,A001`
	filePath := createTestCSV(t, content)
	defer os.Remove(filePath)

	records, err := readAnalyzeCSV(filePath)
	if err != nil {
		t.Fatalf("readAnalyzeCSV failed: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("Expected 1 record after skipping header, got %d", len(records))
	}
	expected := AnalyzeRecord{CharCode: "C001", Char: 'A', AtenaNo: "Item1"}
	if !reflect.DeepEqual(records[0], expected) {
		t.Errorf("Record mismatch:\n Got: %+v\nWant: %+v", records[0], expected)
	}
}

func TestReadAnalyzeCSV_SkipInvalid(t *testing.T) {
	content := `C001,A,Item1,A001
,,,A002
C003,,Item3,A003
C004,D,Item4,
C005,E,Item5` // 列不足
	filePath := createTestCSV(t, content)
	defer os.Remove(filePath)

	records, err := readAnalyzeCSV(filePath)
	if err != nil {
		t.Fatalf("readAnalyzeCSV failed: %v", err)
	}

	// 最初の行だけが有効
	if len(records) != 1 {
		t.Fatalf("Expected 1 valid record, got %d", len(records))
	}
	expected := AnalyzeRecord{CharCode: "C001", Char: 'A', AtenaNo: "Item1"}
	if !reflect.DeepEqual(records[0], expected) {
		t.Errorf("Record mismatch:\n Got: %+v\nWant: %+v", records[0], expected)
	}
}

func TestReadAnalyzeCSV_EmptyFile(t *testing.T) {
	filePath := createTestCSV(t, "")
	defer os.Remove(filePath)

	records, err := readAnalyzeCSV(filePath)
	if err != nil {
		t.Fatalf("readAnalyzeCSV failed for empty file: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("Expected 0 records for empty file, got %d", len(records))
	}
}

// --- writeAnalyzeResult のテスト (簡易版) ---
// CSVの内容を厳密に比較するのは複雑なため、ここではファイルが正常に書き込めるかを確認

func TestWriteAnalyzeResult(t *testing.T) {
	result := map[rune]AtenaNo{'A': "A001", 'B': "A001", 'C': "A002"}
	charToCode := map[rune]CharCode{'A': "C001", 'B': "C002", 'C': "C003"}
	tmpfile, err := os.CreateTemp("", "testwrite_*.csv")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	filePath := tmpfile.Name()
	tmpfile.Close() // Close immediately, writeAnalyzeResult will create it again
	defer os.Remove(filePath)

	err = writeAnalyzeResult(filePath, result, charToCode)
	if err != nil {
		t.Fatalf("writeAnalyzeResult failed: %v", err)
	}

	// ファイルが存在し、中身が空でないことを確認（より厳密なテストも可能）
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Failed to stat output file: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("Output file is empty")
	}
}

// --- bomSkipper (テストは省略、必要なら別ファイルで) ---
// この関数は標準ライブラリのラッパーなので、信頼性は高いと仮定
