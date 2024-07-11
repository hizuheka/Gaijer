package cmd

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// * 調査対象の外字情報の構造体
type Gaiji struct {
	Moji      rune
	Codepoint string
}

// * filename で指定されたファイルから、外字リストを作成する。
// * filename には、UTF16BE、BOMなし、1行に外字1文字が出力されている。
func CreateGaijiList(fileName string) ([]*Gaiji, error) {
	slog.Debug("[createGaijiList] START")
	// ハッシュリストを定義
	// gaijiList := make([]*gaiji, 2000)
	var gaijiList []*Gaiji

	defer func() {
		slog.Info(fmt.Sprintf("[createGaijiList] END : 外字リストの数=%d", len(gaijiList)))
	}()

	// 入力ファイルを開く
	fp, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer fp.Close()

	// 入力ファイルを読み込む。BOMが付いていた場合も考慮。
	reader := bufio.NewReader(transform.NewReader(fp, unicode.BOMOverride(unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder())))
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		// 一行ずつ取得。
		line := scanner.Text()
		if line == "" {
			return nil, fmt.Errorf("空白行が存在します(file=%s)", fileName)
		}
		r := []rune(line)[0] // runeに変換。1文字目だけ取得
		// ハッシュリストにセット
		gaijiList = append(gaijiList, &Gaiji{r, fmt.Sprintf("%X", r)})
	}

	// エラー処理
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return gaijiList, nil
}
