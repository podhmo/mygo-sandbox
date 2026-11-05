# Claude Code のセッションファイル（対話履歴）の調査メモ

`apps/cc-history`（mygo 製の履歴ビューア）を作るために、Claude Code がローカルに残す対話履歴の形式を調べた記録。

- 調査日: 2026-11-05
- Claude Code のバージョン: 手元の記録では 2.1.274 〜 2.1.318
- **非公開の内部形式**なので、バージョンアップで変わりうる。ここに書いたのは観察結果で、公式の仕様ではない。

## 置き場所

```
~/.claude/projects/
  <エンコードしたプロジェクトのパス>/
    <session-id>.jsonl                      ← 1 セッション = 1 ファイル
    <session-id>/subagents/agent-<id>.jsonl ← サブエージェント（Agent ツール）の会話
    memory/                                 ← auto memory（セッションではない）
```

- `CLAUDE_CONFIG_DIR` を設定している場合は `~/.claude` の代わりにそのディレクトリ。
- ディレクトリ名は作業ディレクトリのパスの `/` と `.` を `-` に置き換えたもの。
  例: `/Users/foo/ghq/github.com/podhmo/mygo-sandbox` → `-Users-foo-ghq-github-com-podhmo-mygo-sandbox`。
  **元に戻すと曖昧になる**（`-` がパス区切りなのか元からあった `-` なのか区別できない）ので、元のパスはレコードの `cwd` から取るほうが確実。
- ファイル名（拡張子なし）がセッション ID。`claude --resume <session-id>` で再開できる（再開はそのプロジェクトのディレクトリで実行する）。
- 手元の規模: プロジェクト 26、jsonl 330 ファイル（サブエージェント込み）、合計 519 MB。プロンプトのあるトップレベルのセッションは 150。

## 形式: JSON Lines

1 行 1 レコード。区切りの空白を入れないコンパクトな JSON で、多くのレコードは `"type"` が最初のキー（`{"type":"mode",...}`）。ただし `user` / `assistant` / `attachment` は `parentUuid` が先頭に来る。

- 画像を貼ると base64 がそのまま入るため、**1 行が数 MB になる**ことがある。Go の `bufio.Scanner` は既定のバッファ（64KB）では読めないので、`bufio.Reader.ReadBytes('\n')` などで読む。
- 会話は `uuid` / `parentUuid` で木構造になっている（巻き戻し・分岐があるため）。今回のビューアでは単純にファイル順に並べた。

### 共通フィールド（user / assistant / attachment / system）

| フィールド | 内容 |
|---|---|
| `type` | レコードの種類 |
| `uuid` / `parentUuid` | 会話の木構造 |
| `timestamp` | RFC 3339（UTC） |
| `sessionId` | セッション ID |
| `cwd` / `gitBranch` | そのときの作業ディレクトリ・ブランチ |
| `version` | Claude Code のバージョン |
| `isSidechain` | サイドチェーン（サブエージェントなど）なら true |
| `isMeta` | ユーザーには見せないメタ情報（`<local-command-caveat>` など） |
| `entrypoint` / `userType` | `cli` / `external` など |

## レコードの種類（手元の全データでの件数）

| type | 件数 | 内容 | 履歴表示での扱い |
|---|---|---|---|
| `attachment` | 34268 | 会話に付く添付（hook の結果、リマインダー、環境情報、作業中に送られた入力など） | `attachment.type == "queued_command"` だけ使う |
| `assistant` | 23951 | モデルの出力。**content ブロック 1 つごとに 1 レコード**（同じ `message.id` が複数行に分かれる） | text / thinking / tool_use を表示 |
| `user` | 13108 | ユーザーの入力と **tool_result** | 下記 |
| `api-request` / `api-request-blob` / `api-request-shape` | 約 12900 | API リクエストの記録（system prompt・ツール定義など）。大きい | 読み飛ばす |
| `last-prompt` | 6320 | 最後のプロンプト | 不要 |
| `ai-title` | 5936 | 自動生成のセッションタイトル（`aiTitle`）。何度も更新される → 最後のものを使う | タイトル |
| `custom-title` | 120 | ユーザーが付けたタイトル（`customTitle`） | タイトル（最優先） |
| `agent-name` | 290 | エージェント名（`agentName`） | タイトルの候補 |
| `system` | 3702 | `subtype: "local_command"` など（`/clear` の実行と出力） | 補足として表示 |
| `mode` / `permission-mode` / `atis-latch` / `session-owner` / `cost-state` | | セッションの状態 | 不要 |
| `file-history-snapshot` / `file-history-delta` | | 編集したファイルのバックアップ（巻き戻し用） | 不要 |
| `queue-operation` | 1704 | 作業中に送った入力のキュー操作（`enqueue`） | 使わない（`queued_command` と重複） |
| `pr-link`, `tools-used`, `worktree-state`, `relocated`, `continued-in` など | | その他 | 不要 |

### user レコード

`message.content` は**文字列**のこともあれば**ブロックの配列**のこともある。

```jsonc
// 人間の入力（文字列）
{"type":"user","message":{"role":"user","content":"mygoでguiを試してみたい。..."},
 "origin":{"kind":"human"},"promptSource":"typed", ...}

// ツールの結果（配列）。content は文字列か、text / image ブロックの配列
{"type":"user","message":{"role":"user","content":[
  {"type":"tool_result","tool_use_id":"toolu_01...","content":"total 0\n...","is_error":false}]},
 "toolUseResult":{"stdout":"...","stderr":"",...}, ...}

// 画像付きの入力
{"type":"user","message":{"role":"user","content":[
  {"type":"text","text":"[Image #1]"},
  {"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBOR..."}}]}, ...}
```

`origin.kind` で発生源が分かる（手元の件数）:

| origin.kind | 件数 | 内容 |
|---|---|---|
| （なし） | 11401 | tool_result や、古いバージョンのレコード |
| `human` | 1451 | 人間が打った入力 |
| `task-notification` | 135 | バックグラウンドタスクの完了通知（`<task-notification>...`） |
| `peer` | 114 | 他のセッション・エージェントからのメッセージ |
| `auto-continuation` | 7 | 自動での続行 |

そのほかの特殊な user レコード:

- `isMeta: true` … `<local-command-caveat>` などの説明。表示しない
- `isCompactSummary: true` … `/compact`（またはコンテキスト上限）で前半を要約したもの。本文は「This session is being continued from a previous conversation...」で始まる
- `[Request interrupted by user]` … 中断の印（text ブロック）

**人間のプロンプトの判定**（今回の実装）: `type=="user"` で、`isMeta`・`isCompactSummary` でなく、`origin.kind` が無いか `human` で、tool_result を含まず、`[Request interrupted` / `<local-command-` / `<task-notification>` で始まらないもの。加えて `attachment.type=="queued_command"` の `prompt`。

### assistant レコード

```jsonc
{"type":"assistant","message":{"model":"claude-opus-5-5","id":"msg_...","role":"assistant",
  "content":[{"type":"tool_use","id":"toolu_01...","name":"Bash",
              "input":{"command":"ls -la","description":"リポジトリの中身を確認"}}],
  "stop_reason":"tool_use","usage":{...}}, "requestId":"req_...", ...}
```

- content ブロックの種類: `text` / `thinking` / `tool_use`
- `thinking` は本文が空（`"thinking":""` と `signature` だけ）のことが多い
- `tool_use.id` と、後続の user レコードの `tool_result.tool_use_id` が対応する
- `usage` にトークン数がある（コストの集計にも使えそう）

### attachment レコード

`attachment.type` の上位: `hook_success`, `total_tokens_reminder`, `deferred_tools_record`, `environment`, `silent_turn_reminder`, `queued_command`, `prompt_snapshot`, `edited_text_file`, `skill_listing`, ...

作業中に送ったメッセージは `queued_command` として残る:

```jsonc
{"type":"attachment","attachment":{"type":"queued_command","prompt":"列揃えはしなくて良いや。",
  "origin":{"kind":"human"},"humanTurn":true, ...}, "rendered":[...], ...}
```

## 読み込みの性能

- 全セッションの一覧（タイトル・期間・プロンプト数）を作るには全ファイルを最後まで読む必要がある（`ai-title` は途中で何度も更新され、`custom-title` は末尾に付くことがある）。
- 対策として、行頭が `{"type":"api-request` / `{"type":"file-history` / `{"type":"session-owner"` の行は JSON をデコードせずに捨て、ファイルごとに goroutine で並列に読んだ。
  → **519 MB / 150 セッションで約 0.25 秒**（M 系 Mac）。起動時に毎回読み直しても困らない。
- 1 セッションの会話全体を読むのは数 ms。

## 未対応・気になる点

- `uuid` / `parentUuid` の木をたどっていないので、巻き戻しで捨てた分岐も時系列で混ざって表示される。
- サブエージェントの会話（`<session-id>/subagents/`）は表示していない。
- `--resume` / `--continue` で再開したセッションが別ファイルになる場合の関連付け（`continued-in` レコード）は未調査。
- 形式は非公開なので、Claude Code の更新で壊れる可能性がある。未知の `type` は無視する作りにしている。
