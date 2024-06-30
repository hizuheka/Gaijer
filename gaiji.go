package main

import (
	"bufio"
	"fmt"
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
	gaijList := make([]*gaiji, 2000)

	// 入力ファイルを開く
	fp, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer fp.Close()

	// 入力ファイルを読み込む
	reader := bufio.NewReader(transform.NewReader(fp, unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder()))
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		// 一行ずつ取得。
		r := []rune(scanner.Text())[0] // runeに変換。1文字目だけ取得
		// ハッシュリストにセット
		gaijList = append(gaijList, &gaiji{r, fmt.Sprintf("%x", r)})
	}

	// エラー処理
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return gaijList, nil
}
