# Devin CLI のセッションファイル（対話履歴）の調査メモ

`apps/agent-history` の Devin 取得元を作るため、`~/.local/share/devin` 以下を読み取り専用で調べた記録。

- 調査日: 2026-11-05。手元の CLI: `devin 3000.11.234`。
- **非公開の内部形式**として扱う。以下のフィールド・件数は実データの観察結果であり、公式の仕様ではない。
- 認証情報（`credentials.toml`、`~/.cache/devin`）は読んでいない。

## 置き場所

```text
~/.local/share/devin/                 # XDG_DATA_HOME 側。履歴はここ
  cli/
    sessions.db                       # SQLite (WAL)。セッションと全会話の本体
    sessions.db-wal
    transcripts/<session-id>.json     # ATIF 形式のエクスポート（後述。無いセッションもある）
    summaries/history_<hex>.md        # compaction 時の全メッセージダンプ
    session_locks/<session-id>.lock   # 実行中セッションのロック（中身は PID）
    logs/devin_*.log.gz               # CLI のログ
    trusted_workspaces.json           # 信頼済みディレクトリ
    installation_id, app_state.json, plugins/, ...
  credentials.toml                    # 読まない
~/.config/devin/                      # XDG_CONFIG_HOME 側
  config.json, AGENTS.md
```

- セッション ID は `miniature-order` のような「形容詞-名詞」のランダム名。`sessions.id`、transcript のファイル名、lock のファイル名が一致する。
- 手元の規模: セッション 20、message_nodes 118,968 行、prompt_history 169 行、transcripts 16 ファイル（各 300〜900 KB）、summaries 226 ファイル、sessions.db 約 1.6 GB。

## sessions.db（SQLite）が本体

Claude Code や Codex の JSONL と違い、Devin は**全会話を SQLite に保存**する。WAL モードなので実行中でも読める（読み取りは `mode=ro` で開く）。主要テーブル:

| テーブル | 内容 |
|---|---|
| `sessions` | セッション一覧。`id`, `working_directory`, `backend_type`, `model`, `agent_mode`, `created_at`, `last_activity_at`, `title`, `main_chain_id`, `workspace_dirs`, `hidden`, `metadata` |
| `message_nodes` | 会話ノードの森（後述）。`session_id`, `node_id`, `parent_node_id`, `chat_message`（JSON）, `created_at`, `metadata` |
| `prompt_history` | 人間が打ったプロンプトの履歴。`content`, `timestamp`, `session_id`, `is_shell`（`!` bash モード用と思われるが手元は全て 0） |
| `subagent_heads` | サブエージェントの会話チェーンの先頭。`session_id`, `agent_id`, `chain_node_id` |
| `tool_call_state` | ACP 形式の ToolCall / ToolCallUpdate JSON（エディタ統合の表示用）。`session_id`, `tool_call_id` が PK |
| `message_search` | FTS5 の contentless テーブル。`assistant` の本文と `user` で `is_user_input=1` のものだけがトリガで登録される = 「検索対象 = アシスタント発言と人間のプロンプト」という公式の判定例になっている |
| `live_row_times`, `rendered_commits`, `app_state`, `refinery_schema_history` | UI 表示用・スキーマ管理。`rendered_commits` は手元では空 |

### sessions テーブル

```text
id, working_directory, backend_type, model, agent_mode,
created_at INTEGER,          -- unix 秒
last_activity_at INTEGER,    -- unix 秒
title,                       -- 自動生成 or /rename。手元は日本語タイトル
main_chain_id INTEGER,       -- message_nodes.row_id。現行チェーンの末尾
shell_last_seen_index, cogs_json, workspace_dirs, hidden, metadata
```

- 手元では `backend_type` は全て `windsurf`（エディタ経由の起動）、`agent_mode` は `bypass` がほとんど、`model` は `swe-2-max` / `swe-2-high` / 空。`metadata` は `{"total_credit_cost":0,"total_acu_cost":0.0}` のような JSON。
- `hidden` は手元では全て 0。`devin ls` は既定でカレントディレクトリのセッションだけを出す。
- **git ブランチは保存されていない**。`Session.GitBranch` は空にするか別途取得。

### message_nodes とチェーン構造

`chat_message` は 1 メッセージの JSON。`parent_node_id` で親を指す**木（正確には森）**だが、単純な会話ツリーではない:

- 同じ `message_id` のメッセージが複数ノードに重複して存在する。観察では**リクエストのたびにコンテキスト全体が別チェーンとして書き込まれる**（先頭数ノードだけのチェーンが多数ある）。
- そのため `node_id` 順に読むと同じ発言が何度も出る。**正規の会話は `sessions.main_chain_id`（`row_id`）から `parent_node_id` を遡って復元する**。遡ると system → user → assistant → tool … と時系列の 1 本の列になることを確認した。
- サブエージェントの会話も同じ `message_nodes` に別チェーンで入っており、`subagent_heads.chain_node_id` が各チェーンの先頭（`node_id` として引ける）を指す。
- 分岐は rewind/revert（`/revert`）でも起きる。チェーンを遡る方式なら捨てた分岐は自然に除外できる。

`chat_message` の構造:

```jsonc
{
  "message_id": "d9532b9c-…",
  "role": "assistant",               // system / user / assistant / tool
  "content": "…",                    // 本文テキスト。tool_calls だけのときは空
  "tool_calls": [                    // assistant のみ
    {"id": "exec:1#6aa8bf71…", "name": "exec", "arguments": {"command": "ls …"}, "index": 1, "kind": "function"}],
  "tool_call_id": "…",               // role=tool のとき、対応する tool_calls.id
  "thinking": {"thinking": "…", "signature": "sealed.v1.…"},
  "images": [{"width": 840, "height": 250, "base64_data": "iVBOR…"}],  // 画像は base64
  "phase": "final_answer",           // 一部の assistant に付く
  "metadata": {
    "created_at": "2026-10-31T08:24:56.04…Z",   // RFC 3339 nano
    "is_user_input": true,            // 人間の入力の判定フラグ（後述）
    "request_id", "metrics", "finish_reason", "num_tokens",
    "generation_model", "response_dimensions", "started_generation_at",
    "extensions": {"chisel/client-message-id": "…"},
    "telemetry": {"source": "user", "operation": "unknown"}
  }
}
```

- `role` の内訳（先頭 2 万件）: assistant 10,071、tool 7,797、system 1,636、user 496。
- `tool_calls` の `arguments` は**オブジェクト**（Codex の JSON 文字列ではない）。`id` は `exec:1#<hex>` のように `<tool>:<index>#<hash>`。
- `tool` ロールの `content` がツール結果。`tool_call_id` で結合する。tool 側にも `images` が付くことがある（内容は `"[Image 1]"`）。
- `thinking` には平文 + `signature`。Codex と違い暗号化されていない。
- `message_nodes.metadata`（別カラム）は `'null'` か `{"is_system_prefix":true,"num_tokens_preceding":…,"summarized_from":…}` — compaction 関連の内部フラグ。

### 人間の入力の判定

`role=user` には人間以外のものも混ざる:

- `metadata.is_user_input == 1` → 人間が打ったプロンプト。**これが判定条件**（FTS トリガも同じ条件を使っている）。
- `is_user_input` が null/false の user メッセージの例:
  - `"continue"` — 自動続行のプロンプト（`extensions."barista/prompt_cache_footer"` などが付く）
  - `"Conversation to summarize:\n=== MESSAGE 0 - System ===…"` / `"Now summarize the conversation above…"` — compaction の要約リクエスト。中に `=== MESSAGE n - Role ===` 形式のダンプが埋まっている
- 一覧のプロンプト数・最初のプロンプトは `prompt_history` から取るのが手軽（打った文字そのものが入る。`再開` なども含む）。

## transcripts/\<session-id\>.json（ATIF）

`--export` の出力と同じ **ATIF（[Agent Trajectory Interchange Format](https://github.com/harbor-framework/harbor/blob/main/rfcs/0001-trajectory-format.md), `schema_version: "ATIF-v1.7"`）** の JSON。1 セッション 1 ファイル。

- 手元では **終了したセッションの 15/16 が last_activity 時点までの内容を持つ**（残り 1 件は resume 後の追加分が無い古いファイル）。実行中のセッションと、ごく短いセッション（ノード 26 個程度）にはファイル自体が無かった。**全履歴としては sessions.db を見るのが確実**で、transcript は「あれば使える整形式スナップショット」。
- ルート: `schema_version`, `session_id`, `agent`（`name`, `version`, `model_name`, **`tool_definitions` にツール定義全文** = ファイルが大きい理由）, `steps`, `final_metrics`（合計トークン数）。
- step: `step_id`（1 始まり連番）, `timestamp`（RFC 3339 nano）, `source`（`system` / `user` / `agent`）, `message`, `reasoning_content`, `tool_calls`, `observation`, `metrics`, `model_name`, `extra`。
  - `source=user` は人間のプロンプトのみ（自動 continue は含まれなかった）。
  - `source=agent` の `tool_calls`: `[{tool_call_id, function_name, arguments(オブジェクト)}]`。結果は同じ step の `observation.results[]` に `{source_call_id, content}` で入り、`tool_call_id` で対応する。
  - `source=system` はシステムプロンプト断片（`<system_info>`、rules、available_skills など）が複数 step に分かれて入る。
- サブエージェントの内部会話は含まれない（`run_subagent` が 1 ツール呼び出しとして見えるだけ）。ATIF v1.8 の `subagent_trajectories` に相当するものも無い。

## summaries/history\_\<hex\>.md

compaction（コンテキスト圧縮）時の**全メッセージのダンプ**。形式は `=== MESSAGE N - Role ===` 区切りのテキスト。要約後の継続セッションの system メッセージに「Full conversation history saved at …/history_XXXX.md」と参照され、`<summary>` タグの要約本文が続く。セッション ID ではなくランダム hex で、1 セッションが複数回 compaction すると複数ファイルができる（手元 226 ファイル / 20 セッション）。

## 再開コマンド

```sh
cd '<working_directory>' && devin --resume '<session-id>'
```

- `devin --resume <SESSION_ID>` / `-r` で ID 指定再開。`devin --continue` / `-c` はカレントディレクトリの最新セッション。`/resume` スラッシュコマンド、対話ピッカー `devin ls` もある。
- `devin ls` は既定で cwd のセッションのみなので、再開コマンドは `sessions.working_directory` への `cd` 付きにするのが安全（実際に再開する動作は未検証）。

## agent-history の Source 実装への対応

| 必要なもの | 取り方 |
|---|---|
| セッション一覧 | `sessions` テーブル（`title`, `working_directory`, `created_at`, `last_activity_at`） |
| 会話（Entries） | `main_chain_id` から `parent_node_id` を遡って反転。`role`/`tool_calls`/`tool_call_id`/`thinking`/`metadata.is_user_input` で Kind を決める |
| 人間のプロンプト数 | `prompt_history` か、主チェーンの `is_user_input=1` を数える |
| タイトル | `sessions.title` |
| 再開 | 上記の `devin --resume` |
| GitBranch | 保存されていないので空 |
| サブエージェント | `subagent_heads` のチェーン（今回も一覧から除外してよさそう） |

- SQLite なので JSONL のような全ファイル走査は不要。一覧は 1 クエリで取れ、性能上の工夫はほぼ要らないはず。
- `Size` はファイルサイズが直接無いので、セッションの message_nodes の `length(chat_message)` 合計などで代用する案がある。

## 未対応・不明な点

- transcripts が書かれる正確なタイミングは未検証（終了時 export か逐次か。少なくとも resume 後に更新されないケースあり）。
- `main_chain_id` 以外のチェーン（リクエストごとのスナップショットに見える）の正確な意味は未検証。遡る方式で実害はない。
- `agent_mode` `bypass` 以外、`backend_type` `windsurf` 以外（`--cloud` セッション等）のデータは手元に無い。
- compaction 後の「有効な会話」と `summaries` の厳密な対応、message_nodes.metadata の `summarized_from` の意味は未調査。主チェーンをそのまま表示する限り不要。
- `is_shell` の立つ条件、`shell_last_seen_index`、rewind/revert 後の `main_chain_id` の動きは未検証。
- 形式は非公開なので CLI の更新で壊れる可能性がある。未知の role / フィールドは無視する作りにする。
