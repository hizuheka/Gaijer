package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func main() {
	// * 外字リストファイルを読み込み、外字ハッシュマップ（HashMap（外字、外字構造体））を作成する
	gaijiList, err := createGaijiList("gaijilist.txt")
	if err != nil {
		panic(err)
	}
	fpIn, err := os.Open("input.txt")
	if err != nil {
		panic(err)
	}
	defer fpIn.Close()

	for {
		reader := bufio.NewReaderSize(fpIn, 1000*10)
		chunk, err := readFileByChunk(reader, 1000)
		if err != nil {
			break
		}
		// todo 外字ハッシュリストの、外字構造体の使用有無がfalseのものを対象に、繰り返し処理する
		for _, v := range gaijiList {
			if !v.used {
				// todo 検索対象の100バイトの中に、外字が含まれるかを調べる
				// * 読み込んだバイト配列の中に、外字が含まれるかを調べる。
				// * 読み込んだバイト配列の2バイト文字はリトルエンディアンとなっているため、
				// * 外字はリトルエンディアンに変換する。
				b := [2]byte{}
				copy(b[:], []byte(string(v.moji)))
				if contains(chunk, swap(b)) {
					// * 外字が含まれた場合は、外字構造体の使用有無に、trueをセットする
					v.used = true
				}
			}
		}
	}

	for n, v := range gaijiList {
		fmt.Printf("%v:%s-%v", n, string(v.moji), v.used)
	}
}

// readFileByChunkは、指定されたファイルを指定されたサイズ chunkSize で分割して読み取ります
func readFileByChunk(r *bufio.Reader, chunkSize int) ([]byte, error) {
	buffer := make([]byte, chunkSize)
	n, err := r.Read(buffer)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		panic(err)
	}
	fmt.Println(string(buffer[:n]))
	return buffer[:n], nil
}

// * swapはバイトの配列を受け取り、配列の要素の順番を反転します。
func swap(a [2]byte) [2]byte {
	a[0], a[1] = a[1], a[0]
	return a
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
		gaijList = append(gaijList, &gaiji{r, fmt.Sprintf("%x", r), false})
	}

	// エラー処理
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return gaijList, nil
}

// * containsはb1内にb2があるかどうかを判断します
func contains(b1 []byte, b2 [2]byte) bool {
	return bytes.Contains(b1, b2[:])
}

// * 調査対象の外字情報の構造体
type gaiji struct {
	moji      rune
	codepoint string
	used      bool // 使用有無
}
