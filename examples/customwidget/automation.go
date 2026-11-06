//go:build e2e

package main

// e2e タグを付けたビルドだけ automation プラグインを入れる。
// MYGO_AUTOMATION=<socket のパス> を渡して起動したときだけ待ち受ける。
import _ "github.com/egoist/mygo/plugins/automation"
