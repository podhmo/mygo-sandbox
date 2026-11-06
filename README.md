# mygo-sandbox

[mygo](https://mygo.egoist.dev/)（Go でデスクトップアプリを作るフレームワーク）を試すためのリポジトリ。

## 必要なもの

- Go 1.27.3 以上（native UI なら Go だけで足りる）

## 中身

| パス | 内容 |
|---|---|
| [`examples/`](examples) | native UI の小さな例: `counter`（最小）、`todo`（入力・リスト）、`async`（goroutine からの更新）、`customwidget`（mygo に無い部品を自作する: つまみ、ヒートマップ、ToggleBase のチップ） |
| [`apps/agent-history/`](apps/agent-history) | Claude Code / Codex / Devin / OpenCode の過去のセッションを一覧して読むビューア。マスキング、マークダウンでのコピーに対応 |
| [`apps/agent-history/nsgo/`](apps/agent-history/nsgo) | 同じビューアの UI を [nsgo](https://github.com/oneo-me/nsgo) の `ui` 部品で書き直したもの（元の機能はひととおり） |
| [`docs/sketch/ja/`](docs/sketch/ja) | 調査メモ（下記） |

## 使い方

```sh
go run ./examples/counter
go run ./apps/agent-history
go test ./...                          # ウィンドウを開かずに UI もテストする
go tool mygo dev apps/agent-history    # live reload で開発
go tool mygo build apps/agent-history  # .app / .dmg を作る
```

## 調査メモ

- [mygo.md](docs/sketch/ja/mygo.md): mygo の概要と書き方
- [mygo-examples.md](docs/sketch/ja/mygo-examples.md): examples やアプリを作って分かったこと
- [mygo-testing.md](docs/sketch/ja/mygo-testing.md): mygo のテストの仕方（Tester と automation プラグインでの実ウィンドウ E2E）とスクリーンキャプチャ。nsgo との比較
- [mygo-custom-widgets.md](docs/sketch/ja/mygo-custom-widgets.md): mygo に無い部品の作り方（Draw・入力・base・アクセシビリティ）と、作って動かした記録
- [nsgo.md](docs/sketch/ja/nsgo.md): nsgo（NSH の Go 移植、ネイティブビューで描く UI エンジン）の調査、事前準備、examples、agent-history の試作
- [linux-wsl.md](docs/sketch/ja/linux-wsl.md): WSL2 + WSLg（Ubuntu 26.04）での追試。動いたもの・動かないもの、GTK4 のローカル展開、GPU（d3d12）の話
- [claude-code-session-files.md](docs/sketch/ja/claude-code-session-files.md) / [codex-session-files.md](docs/sketch/ja/codex-session-files.md) / [devin-session-files.md](docs/sketch/ja/devin-session-files.md) / [opencode-session-files.md](docs/sketch/ja/opencode-session-files.md): セッション履歴ファイルの形式
- [agent-history-masking.md](docs/sketch/ja/agent-history-masking.md): マスキング対象の定義
- [agent-history-codex-task.md](docs/sketch/ja/agent-history-codex-task.md): Codex 対応を Codex に依頼したときの依頼文
