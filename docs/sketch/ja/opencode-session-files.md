# OpenCode のセッションファイル（対話履歴）の調査メモ

`apps/agent-history` の OpenCode 取得元を作るため、`~/.local/share/opencode` 以下を読み取り専用で調べた記録。

- 調査日: 2026-11-05。手元の CLI: `opencode v2.2.5`（セッションには `version` 列に 2.1.3 / 2.2.5 が記録されている）。
- **非公開の内部形式**として扱う。以下のフィールド・件数は実データの観察結果であり、公式の仕様ではない。
- `account` / `credential` / `control_account` テーブルには認証トークンが平文で入る。**読まない**。

## 置き場所

```text
~/.local/share/opencode/
  opencode.db                    # SQLite (WAL)。セッション・メッセージの本体
  opencode.db-wal, -shm
  log/                           # CLI ログ
  shell/<project-id>/sh_*.out    # ユーザーの ! シェル実行の出力
  snapshot/<project-id>/<sha>    # ファイルスナップショット（revert 用）
  worktree/<project-hash>/<name> # opencode 管理の git worktree
  repos/, tool-output/           # 手元では空（tool-output は参照 3 件のみ）
```

- OpenCode はサーバ型（`opencode` 起動でバックグラウンド service が動く）。履歴は全て DB。WAL モードなので実行中でも `mode=ro` で読める。
- 手元の規模: セッション 25（うちサブエージェント 4）、session_message 3,226 行、project 8、db 46 MB。

## sessions（`session_v2` テーブル）

```text
id                ses_e54202971ffeVC4jqoEwY2O6Po   -- ses_ 接頭辞 + ULID 風
slug              glowing-pixel                     -- 形容詞-名詞の自動名（Devin の id と同じ命名）
project_id        <project.id>
parent_id         -- サブエージェントのセッションは親の id（後述）
fork_session_id, fork_boundary                      -- fork 元（手元では未使用）
directory         -- 実行ディレクトリ（cwd。worktree 内のこともある）
title             -- 自動生成タイトル。NULL あり
version           -- 使われた CLI バージョン
agent             -- 'build'（通常）/ 'general'（サブエージェント）
model             -- {"id":"deepseek-v4.1-flash-free","providerID":"opencode","variant":"high"}
cost, tokens_input/output/reasoning/cache_read/cache_write
summary_additions/deletions/files/diffs             -- diff 統計
revert, permission, metadata, share_url
time_created, time_updated, time_idle, time_archived, ...
```

- **時刻は全て unix epoch ミリ秒**（Devin の `sessions` は秒だったので注意）。
- プロジェクトの対応: `project` テーブルが `worktree` パス → `id`（ハッシュ）。ただし `session_v2.directory` に cwd がそのまま入るので一覧にはそれを使う。`shell/` `snapshot/` のディレクトリ名は project_id。
- サブエージェントは**別 session_v2 行**で `parent_id` が親を指し、`agent='general'` になる（Devin は同一 DB の別チェーンだったが、こちらは別セッション）。`opencode session list` が "top-level sessions" と言っているので、一覧では `parent_id IS NULL` を基本条件にする。
- worktree 内で動いたセッションは `directory` が `~/.local/share/opencode/worktree/...` を指す。プロジェクトのまとめには `project.worktree` 側を見た方がよいかもしれない。

## session_message（会話の本体）

`id`（`msg_…`）, `session_id`, `type`, `seq`, `time_created/updated`, `data`（JSON）。`seq` でソートする（間は飛ぶ）。

`session_part` テーブルもあるが手元では **0 行**。観察したバージョン（2.1.3〜2.2.5）では content ブロックは `data.content` に内蔵される。

### type の内訳（手元 3,226 件）

| type | 件数 | 内容 | 履歴表示での扱い |
|---|---|---|---|
| `assistant` | 2,893 | モデルの出力。`data.content` がブロック配列 | 下記 |
| `user` | 151 | 人間の入力 | `KindUser` |
| `synthetic` | 44 | シェル実行結果などの合成メッセージ | 補足 |
| `idle` | 118 | アイドル遷移。`outcome: succeeded/failed`（failed 時は error あり） | 不要〜補足 |
| `compaction` | 10 | コンテキスト圧縮。`summary` に Markdown 要約 | `KindSummary` |
| `shell` | 4 | ユーザー自身の `!` シェル実行 | `KindTool` か補足 |
| `system` | 3 | "Instructions updated: core/date" など | 補足 |
| `model-switched`, `location-switched` | 少数 | 状態変化 | 不要 |

### user

```jsonc
{"time":{"created":1792234928872},
 "text":"https://github.com/… を手伝ってほしい…",
 "files":[{"data":"iVBORw0K…"}],   // 添付画像などは base64 インライン
 "agents":[]}                       // @メンション?
```

- **`type='user'` は全て人間の入力**だった（Devin の is_user_input 判定や Codex の自動挿入フィルタは不要に見える）。「再開」などもそのまま入る。
- キューイング: `session_inbox` テーブルに未配送メッセージが入る（`delivery='steer'` = 作業中に送った指示）。配送後は user/synthetic メッセージになると思われるが、作業中フラグ（`Entry.Queued`）への対応づけは未検証。

### assistant

```jsonc
{"time":{"created":…,"streamed":…,"completed":…},
 "agent":"build",
 "model":{"id":"…","providerID":"opencode","variant":"high"},
 "content":[
   {"type":"reasoning","text":"…","state":{"reasoningField":"reasoning_content"},"time":{…}},
   {"type":"tool","id":"call_00_rhlj…","name":"shell","executed":false,
    "state":{"status":"completed","input":{"command":"gh issue view 39 …"},
             "content":[{"type":"text","text":"…結果…"}],
             "metadata":{"status":"completed","truncated":false,"exit":1}},
    "time":{"created":…,"ran":…,"completed":…}},
   {"type":"text","text":"…"}],
 "finish":"tool-calls","cost":…,"tokens":{…},
 "snapshot":{"start":"<sha>","end":"<sha>"}}
```

- content ブロック: `tool` 3,206、`reasoning` 2,367、`text` 381。
- **ツール呼び出しと結果が 1 ブロックに同居**（`state.input` が引数、`state.content` が結果テキスト配列、`state.metadata.exit` が終了コード）。call_id で結合する必要がない。
- `finish`: `tool-calls` 2,770 / `stop` 104 / `error` 19。
- ツール名 top: `shell` 2,199、`edit` 438、`read` 284、`write` 190、`webfetch` 25、`execute` 19、`grep` 17、`skill` 16、`question` 11、`subagent` 4、`websearch` 3。
- `snapshot.start/end` は `~/.local/share/opencode/snapshot/<project_id>/<sha>` に対応するファイルスナップショット（revert 用。ビューアでは不要）。
- `reasoning` は平文（signature なし・暗号化なし）。

### その他の type

- `compaction`: `{"reason":"auto","status":"completed","summary":"## Objective\n…","cost":…,"tokens":…}` — 要約は「## Objective / ## Requirements / ## Decisions / …」形式の Markdown。`KindSummary` にできる。
- `shell`: `{"command":"git switch main && git pull","exit":0,"output":{"output":"…"},"shellID":"sh_…","metadata":{"background":true}}` — ユーザーが `!` で実行したシェル。出力ファイルは `shell/<project_id>/<shellID>.out`。
- `synthetic`: `{"metadata":{"source":"shell","shellID":"…","exit":0},"text":"The following shell command was executed by the user:\n<shell …>…"}` — ユーザー操作をモデルへ伝えるために合成されたメッセージ。
- `system`: `{"metadata":{"notice":"instructions","instructionSources":["core/date"]},"text":"Today's date is now: …"}` — 指示系の更新通知。
- `idle`: `{"outcome":"succeeded"}` / `{"outcome":"failed","error":{"type":"provider.f…"}}` — セッションの終端イベントに近い。provider エラーは補足として出せる。

## 再開コマンド

```sh
cd '<directory>' && opencode --session 'ses_e54202971ffeVC4jqoEwY2O6Po'
```

- `opencode --session <id>` / `-s` で指定再開（なければその ID で新規作成）。`opencode --continue` / `-c` は直近のセッション。
- `opencode session list` は**カレントプロジェクトの top-level セッションだけ**を出すので、`session_v2.directory` への `cd` 付きが安全（実際に再開する動作は未検証）。
- `opencode session export` / `import` で JSON 形式のエクスポートもある（中身は未確認）。

## agent-history の Source 実装への対応

| 必要なもの | 取り方 |
|---|---|
| セッション一覧 | `session_v2 WHERE parent_id IS NULL`（`directory`, `title`, `time_created`, `time_updated`） |
| 会話 | `session_message ORDER BY seq`。user→`KindUser`、assistant の content ブロックを text/reasoning/tool に分解（tool はブロック内で完結）、compaction→`KindSummary`、synthetic/system/idle→補足か無視 |
| 人間のプロンプト | `type='user'` をそのまま使える |
| タイトル | `session_v2.title`（NULL のとき最初のプロンプトにフォールバック） |
| 再開 | 上記 `opencode --session` |
| GitBranch | セッションには無い。project.worktree 経由で取るか空 |
| サブエージェント | `parent_id IS NOT NULL` の行を一覧から除外 |
| Size | `length(data)` の合計などで代用 |

## 未対応・不明な点

- `session_part` は手元では空だが、別バージョン/別条件で使われる可能性（使われる場合はこちらが content の本拠になる）。
- `session_inbox` 配送後の形（作業中メッセージの `Queued` 判定）は未検証。手元の 1 件は `delivery='steer'` の synthetic。
- fork（`fork_session_id`/`fork_boundary`）、`revert` の実データは無し。
- `opencode session export` の出力形式、`share_url`、web 版との違いは未調査。
- worktree セッションの `directory` と `project.worktree` の表示上の正規化方針は要検討。
- 時刻が ms（他の取得元は秒/RFC3339 なので変換を忘れない）。
- 形式は非公開なのでバージョン更新で変わりうる。`version` 列があるので分岐は可能。
