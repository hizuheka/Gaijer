package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/google/subcommands"
)

func main() {
	subcommands.Register(subcommands.HelpCommand(), "")
	subcommands.Register(subcommands.FlagsCommand(), "")
	subcommands.Register(subcommands.CommandsCommand(), "")
	subcommands.Register(&findCmd{}, "")
	subcommands.Register(&sqlCmd{}, "")

	flag.Parse()
	ctx := context.Background()
	os.Exit(int(subcommands.Execute(ctx)))

	// fpIn, err := os.Open("input.txt")
	// if err != nil {
	// 	panic(err)
	// }
	// defer fpIn.Close()

	// for {
	// 	reader := bufio.NewReaderSize(fpIn, 1000*10)
	// 	chunk, err := readFileByChunk(reader, 1000)
	// 	if err != nil {
	// 		break
	// 	}
	// 	// todo 外字ハッシュリストの、外字構造体の使用有無がfalseのものを対象に、繰り返し処理する
	// 	for _, v := range gaijiList {
	// 		if !v.used {
	// 			// todo 検索対象の100バイトの中に、外字が含まれるかを調べる
	// 			// * 読み込んだバイト配列の中に、外字が含まれるかを調べる。
	// 			// * 読み込んだバイト配列の2バイト文字はリトルエンディアンとなっているため、
	// 			// * 外字はリトルエンディアンに変換する。
	// 			b := [2]byte{}
	// 			copy(b[:], []byte(string(v.moji)))
	// 			if contains(chunk, swap(b)) {
	// 				// * 外字が含まれた場合は、外字構造体の使用有無に、trueをセットする
	// 				v.used = true
	// 			}
	// 		}
	// 	}
	// }

	// for n, v := range gaijiList {
	// 	fmt.Printf("%v:%s-%v", n, string(v.moji), v.used)
	// }
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

// * containsはb1内にb2があるかどうかを判断します
func contains(b1 []byte, b2 [2]byte) bool {
	return bytes.Contains(b1, b2[:])
}
