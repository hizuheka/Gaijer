package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// * 調査対象の外字情報の構造体
type gaiji struct {
	moji      rune
	codepoint string
}

// * filename で指定されたファイルから、外字リストを作成する。
// * filename には、UTF16BE、BOMなし、1行に外字1文字が出力されている。
func createGaijiList(fileName string) ([]*gaiji, error) {
	// ハッシュリストを定義
	// gaijiList := make([]*gaiji, 2000)
	var gaijiList []*gaiji

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
		r := []rune(scanner.Text())[0] // runeに変換。1文字目だけ取得
		// ハッシュリストにセット
		gaijiList = append(gaijiList, &gaiji{r, fmt.Sprintf("%x", r)})
	}

	// エラー処理
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	slog.Info(fmt.Sprintf("外字リストを読み込みました。(外字リスト数=%d)", len(gaijiList)))

	return gaijiList, nil
}
