# Codex への依頼: agent-history の Codex 対応

`apps/agent-history` に Codex（OpenAI Codex CLI）のセッション履歴を読む取得元を追加するための依頼文。
Claude Code から Codex に渡した内容をそのまま残す（Codex に直接渡す場合もこの本文を使える）。

---

リポジトリ `github.com/podhmo/mygo-sandbox`（作業ディレクトリはこのリポジトリのルート）で作業してください。

## 背景

`apps/agent-history` は、コーディングエージェントの過去のセッションを一覧して読む、Go 製のデスクトップアプリです（GUI は egoist/mygo の native UI）。今は Claude Code の履歴だけを読めます。取得元は `internal/history.Source` インターフェースで差し替えられる構成になっています。これに **Codex CLI のセッション履歴** を追加してください。

まず次を読んでください:

- `apps/agent-history/README.md`: 構成と「取得元を追加する手順」。この手順に従ってください
- `apps/agent-history/internal/history/history.go`: `Source` / `Session` / `Entry` の定義
- `apps/agent-history/internal/claudecode/`: 既存の実装例（並列読み込み、fixture、テスト）
- `docs/sketch/ja/claude-code-session-files.md`: Claude Code の形式の調査メモ。Codex の調査メモはこれと同じ粒度・書き方（日本語）でお願いします

## やってほしいこと

1. **Codex の保存形式を調べて `docs/sketch/ja/codex-session-files.md` にまとめる**
   - 手元の実データ（`~/.codex/sessions/`、`~/.codex/archived_sessions/` など。`CODEX_HOME` があればその下）を観察する。Codex CLI のソースや公式ドキュメントで確認できたことは出典を書く
   - 少なくとも次を書く: 置き場所とファイル名の規則、行の形式、レコードの種類と件数（手元の集計）、人間の入力・アシスタントの返答・推論・ツール呼び出しと結果の見分け方、作業ディレクトリ・ブランチ・タイトル（相当するものがあれば）の取り方、再開コマンド（`codex resume <id>` など。実際に有効なものを確認する）、サブエージェントや分岐があればその扱い、未対応・不明な点
   - 非公開の内部形式なら、そう明記する。調べた Codex CLI のバージョンを書く
2. **`apps/agent-history/internal/codex/` に `history.Source` を実装する**
   - `ID()` = `"codex"`、`Name()` = `"Codex"`、`AssistantName()` = `"Codex"`
   - 人間の入力が無いセッションは `Sessions()` に含めない。保存先が無いときは `nil, nil`
   - Codex が会話に自動で差し込むもの（環境情報、AGENTS.md の指示、システム的なメッセージなど）は `KindUser` にしない。表示する価値が無ければ捨て、補足として残すなら `KindNotice` にする
   - タイトルは、相当する情報が無ければ最初の人間の入力を `history.OneLine(text, 80)` したもの
   - ツール呼び出しとその結果は 1 つの `KindTool` にまとめる（Claude Code 版の `toolIndex` と同じ考え方）。結果は `history.TruncateResult` で切り詰める
3. **テスト**: `internal/codex/testdata/` に、実データの構造を元に**小さく作り直した** fixture を置き、`codex_test.go` で `Sessions()` と `Entries()` と `ResumeCommand()` を確認する。fixture に実際の会話内容・個人情報・トークン・メールアドレスを入れないこと
4. **組み込み**: `apps/agent-history/main.go` の `options` に `-codex-root`（既定値は Codex の保存先）を、`sources()` に `codex.New(...)` を足す。`apps/agent-history/README.md` の対応表を更新する
5. 確認: `go vet ./...` と `go test ./...` が通ること。実データでの確認として、一時的なテスト（コミットしない）などで `codex.New("").Sessions()` が手元のセッションを読めること、件数と所要時間を確かめ、調査メモに書く

## 制約

- `internal/history` のインターフェースは原則変えない。変える必要がある場合は最小限にし、理由を報告する
- UI（`main.go` の view 部分）は原則触らない
- `~/.codex` の中身は読むだけ。書き換えない
- 依存を増やさない（標準ライブラリと既存の依存だけ）
- コミットはしなくてよい（Claude Code 側で確認してからコミットする）

## 報告してほしいこと

- 変更したファイルの一覧
- 形式について分かったことの要点と、分からなかったこと
- 実データでの件数と所要時間
- `go test ./...` の結果
