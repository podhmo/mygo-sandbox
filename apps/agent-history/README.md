# agent-history

コーディングエージェントの過去のセッション（対話履歴）を一覧して読む、[mygo](https://mygo.egoist.dev/) 製のデスクトップアプリ。

```sh
go run ./apps/agent-history                     # 普通に起動
go tool mygo dev apps/agent-history             # live reload で開発
go tool mygo build apps/agent-history           # .app / .dmg を作る
go test ./apps/agent-history/...                # ウィンドウ無しでテスト
```

対応している取得元:

| 取得元 | パッケージ | 保存先 | 形式の調査メモ |
|---|---|---|---|
| Claude Code | `internal/claudecode` | `~/.claude/projects`（`-claude-root` で変更） | [claude-code-session-files.md](../../docs/sketch/ja/claude-code-session-files.md) |
| Codex | `internal/codex` | `$CODEX_HOME` または `~/.codex`（`-codex-root` で変更） | [codex-session-files.md](../../docs/sketch/ja/codex-session-files.md) |
| Devin | `internal/devin` | `$XDG_DATA_HOME/devin` または `~/.local/share/devin` の `cli/sessions.db`（`-devin-root` で変更） | [devin-session-files.md](../../docs/sketch/ja/devin-session-files.md) |
| OpenCode | `internal/opencode` | `$XDG_DATA_HOME/opencode` または `~/.local/share/opencode` の `opencode.db`（`-opencode-root` で変更） | [opencode-session-files.md](../../docs/sketch/ja/opencode-session-files.md) |

Devin と OpenCode は SQLite の DB に履歴を持つ。実行中でも読めるよう読み取り専用（`mode=ro`）で開き、書き込みはしない。

## マークダウンとしてコピー

会話ヘッダーの「Copy as Markdown」から、範囲を選んでクリップボードにコピーする。

| 項目 | 含めるもの |
|---|---|
| Prompts only | 人間の入力だけ（番号付き） |
| Prompts and replies | 入力とアシスタントの返答（続けて並ぶ返答は 1 つの見出しにまとめる） |
| Prompts, replies and thinking | 上に加えて thinking（`<details>` で折りたたむ） |

- 先頭にタイトル、エージェント、プロジェクト、ブランチ、日時、セッション ID を書く。
- 話し手が替わるところ（2 つ目以降の見出しの前）は `---` の線で区切る。
- ツール呼び出し、補足（中断など）、compact の要約は含めない。画面の Tools / Thinking の切り替えには左右されない。
- **マスクがオンなら、伏せた内容をコピーする**。
- 変換は `history.Markdown`（`internal/history/markdown.go`）。

## マスキング

認証情報・個人情報・環境を特定できる情報（ホームディレクトリ、ユーザー名、ホスト名など）を伏せて表示する。**既定でオン**で、サイドバーの `Mask` で切り替える。対象の定義は [agent-history-masking.md](../../docs/sketch/ja/agent-history-masking.md)。

```sh
go run ./apps/agent-history -mask-words podhmo,example-corp   # 追加で伏せる語（GitHub のユーザー名、会社名など）
go run ./apps/agent-history -mask=false                       # 起動時はオフ
```

- 表示だけを変える（元のファイルは書き換えない）。「Copy resume command」でクリップボードに入れるコマンドは、そのまま使えるよう伏せない。
- パターンでの検出なので完全ではない。公開する資料に使うときは目でも確認すること。

## 構成

```
apps/agent-history/
  main.go                 UI（mygo native UI）。history の型だけを見る
  main_test.go            UI のテスト（偽の Source を使う）
  internal/history/       取得元によらない型: Source, Session, Entry, Project / 共通の小道具
  internal/claudecode/    Claude Code の Source 実装と testdata
  internal/codex/         Codex の Source 実装と testdata
  internal/devin/         Devin の Source 実装（sessions.db を読む）
  internal/opencode/      OpenCode の Source 実装（opencode.db を読む）
  internal/sqlitedb/      SQLite を読み取り専用で開く小道具（modernc.org/sqlite, cgo 不要）。sqlitetest はテスト用の DB 作成
  internal/redact/        表示用のマスキング
```

- UI は `history.Source` のインターフェースだけに依存する。セッションの一覧（`Sessions`）、会話（`Entries`）、再開コマンド（`ResumeCommand`）を Source が返す。
- プロジェクトは**作業ディレクトリ（`Session.Cwd`）でまとめる**。取得元が違っても同じリポジトリのセッションは同じ見出しの下に並ぶ。
- 取得元が 2 つ以上あると、サイドバーに取得元で絞り込む切り替え（All / Claude Code / ...）が出る。

## 取得元を追加する手順

1. 保存形式を調べて `docs/sketch/ja/<agent>-session-files.md` にまとめる（`claude-code-session-files.md` と同じ粒度で）。
2. `internal/<agent>/` を作り、`history.Source` を実装する。
   - `Sessions()`: 全セッションの要約。**人間の入力が無いセッションは含めない**。保存先が無ければ `nil, nil`。ファイル数が多いので並列に読む（`claudecode` 参照）
   - `Entries(s)`: 会話を時系列で。種類の対応づけは下の表
   - `ResumeCommand(s)`: 再開コマンド。無ければ `""`（ボタンが出なくなる）
   - JSON Lines を読むときは `history.EachLine` を使う（1 行が数 MB になることがあるため）。SQLite は `sqlitedb.OpenReadOnly` で開く。ツールの結果は `history.TruncateResult` で切り詰める
3. `internal/<agent>/testdata/` に**実データを元に小さく作った** fixture を置き、`<agent>_test.go` を書く（個人情報や秘密を含めないこと）。SQLite の場合はテストの中で `sqlitetest.Create` を使って DB を作る（バイナリを置かない）。
4. `main.go` の `options` に保存先のフラグを、`sources()` に `<agent>.New(...)` を足す。
5. この README の表を更新する。

`history.Entry` の種類:

| Kind | 用途 | 表示 |
|---|---|---|
| `KindUser` | 人間の入力。作業中に送られたものは `Queued: true` | 枠付きで "You" |
| `KindAssistant` | アシスタントの返答 | Markdown。ラベルは `AssistantName()` |
| `KindThinking` | 推論・推論の要約 | 既定で非表示、折りたたみ |
| `KindTool` | ツール呼び出し（`Tool`, `Text` = 入力の要約, `Result`, `IsError`） | 既定で非表示、折りたたみ |
| `KindNotice` | 中断・通知・システムからの補足 | 小さい灰色の 1 行 |
| `KindSummary` | compact などで前半を要約したもの | 折りたたみ |
