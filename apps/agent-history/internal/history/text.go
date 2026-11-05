package history

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// EachLine は JSON Lines を 1 行ずつ fn に渡す。画像を base64 で含む行は数 MB に
// なるので bufio.Scanner (既定 64KB 上限) ではなく ReadBytes で読む。空行は飛ばす。
// fn に渡す line は次の呼び出しまでしか有効でないとは限らないが、保持しないこと。
func EachLine(r io.Reader, fn func(line []byte)) error {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			fn(line)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// MaxResult はツール結果を表示用に切り詰める長さ (バイト)。
const MaxResult = 4000

// TruncateResult は長いツール結果を切り詰め、残りのバイト数を付ける。
func TruncateResult(s string) string {
	if len(s) <= MaxResult {
		return s
	}
	return Truncate(s, MaxResult) + fmt.Sprintf("\n… (%d bytes more)", len(s)-MaxResult)
}

// Truncate は UTF-8 の文字境界を壊さずに n バイト以下に切り詰める。
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// OneLine は空白をまとめて 1 行にし、n 文字を超えたら … で切る。
func OneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// FirstNonEmpty は空白以外を含む最初の文字列を返す。
func FirstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
