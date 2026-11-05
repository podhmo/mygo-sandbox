# mygo-sandbox

[mygo](https://mygo.egoist.dev/)（Go でデスクトップアプリを作るフレームワーク）を試すためのリポジトリ。

## 必要なもの

- Go 1.27.3 以上（native UI なら Go だけで足りる）

## 中身

| パス | 内容 |
|---|---|
| [`examples/`](examples) | native UI の小さな例: `counter`（最小）、`todo`（入力・リスト）、`async`（goroutine からの更新） |
| [`apps/agent-history/`](apps/agent-history) | Claude Code / Codex / Devin / OpenCode の過去のセッションを一覧して読むビューア。マスキング、マークダウンでのコピーに対応 |
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
- [claude-code-session-files.md](docs/sketch/ja/claude-code-session-files.md) / [codex-session-files.md](docs/sketch/ja/codex-session-files.md) / [devin-session-files.md](docs/sketch/ja/devin-session-files.md) / [opencode-session-files.md](docs/sketch/ja/opencode-session-files.md): セッション履歴ファイルの形式
- [agent-history-masking.md](docs/sketch/ja/agent-history-masking.md): マスキング対象の定義
- [agent-history-codex-task.md](docs/sketch/ja/agent-history-codex-task.md): Codex 対応を Codex に依頼したときの依頼文
