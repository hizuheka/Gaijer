package cmd

import (
	"fmt"
	"log/slog"
)

func CheckError(errChan <-chan error) error {
	slog.Debug("[error check] START")
	select {
	case e, ok := <-errChan:
		if ok { // エラーを取得できた場合。closeされていた場合は、okはfalseになる
			slog.Error("[error check] END : ERROR!!")
			return e
		}
	default:
		slog.Error("[error check] errChanがcloseされていません")
		return fmt.Errorf("errChanがcloseされていません")
	}

	slog.Debug("[error check] END : NO ERROR")
	return nil
}
