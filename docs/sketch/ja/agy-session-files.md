# Antigravity CLI（agy）のセッションファイル（対話履歴）の調査メモ

`apps/agent-history` に agy の取得元を作る前の下調べとして、ローカルの保存データを読み取り専用で調べた記録。

- 調査日: 2026-11-07（JST）。
- 手元の CLI: `agy` 1.5.2（`agy --version`）。`~/.local/bin/agy`。
- **公開仕様として互換性を保証された保存フォーマットではない内部形式**として扱う。以下のフィールド・件数は実データの観察結果であり、公式の仕様ではない。
- 手元にある会話は **2 件だけ**（227 ステップと 18 ステップ）。件数の少なさから、観察できていない形式が多いはず。
- 調査対象は `~/.gemini/antigravity-cli/` の `brain/`、`conversations/`、`history.jsonl`、`cache/`、`conversation_summaries.db`。`settings.json`、`installation_id`、MCP 設定などの認証・設定まわりは読んでいない。
- SQLite はコピーを `?immutable=1` で開いた。元ファイルを直接開こうとすると `unable to open database file` になった（稼働中の CLI が開いているためと思われるが、原因は未確認）。コピーは調査後に削除した。

## 置き場所

```text
~/.gemini/
  AGENTS.md                              # グローバルの指示ファイル
  config/                                # mcp_config.json, projects/*.json
  antigravity-cli/                       # summary の AppDataDir = "antigravity-cli"
    brain/<CONVERSATION_ID>/
      .system_generated/
        logs/
          transcript.jsonl               # 長い値を切り詰めた版
          transcript_full.jsonl          # 切り詰めのない版
          chunks/transcript/NNNNNNNN.jsonl       # 上の 2 つを 100 KiB ごとに分けたもの
          chunks/transcript_full/NNNNNNNN.jsonl
        steps/<STEP_INDEX>/output.txt    # 長すぎたツール出力の全文
        messages/<UUID>.json             # システムからの通知
        messages/read.json               # 通知の既読状態
      .user_uploaded/                    # 空（今回）
      scratch/                           # 空（今回）
    conversations/<CONVERSATION_ID>.db   # SQLite。ステップ本体は protobuf の blob
    conversation_summaries.db            # SQLite。一覧用の要約と FTS5 索引
    history.jsonl                        # 入力履歴（プロンプトとスラッシュコマンド）
    cache/conversation_metadata.json     # 会話の要約（タイトル・プレビュー・ステップ数など）
    cache/last_conversations.json        # ワークスペース → 最後の会話 ID
    annotations/<CONVERSATION_ID>.pbtxt  # title:"..." だけ
    presence/<CONVERSATION_ID>.lock
    implicit/<UUID>.pb                   # バイナリ。中身は未調査
    jetski_state.pbtxt                   # オンボーディング状態など
    log/cli-YYYYMMDD_HHMMSS.log, cli.log
```

- ディレクトリ名の `.gemini` は Gemini CLI と同じだが、agy のデータは `antigravity-cli/` の下にある。Gemini CLI の `~/.gemini/tmp/<hash>/chats/` のような構造はこの環境にはなかった。
- CLI ログには `~/.gemini/antigravity/conversations` を読もうとして失敗した記録がある（`Failed to read conversations directory`）。IDE 版などの別アプリが `antigravity/` を使うと思われる。会話の要約の `AppDataDir` が `"antigravity-cli"` なので、取得元はこのディレクトリ名を区別できるようにしておく。
- 会話 ID は UUID。`brain/`、`conversations/`、`annotations/`、`presence/` で同じ ID がファイル名・ディレクトリ名になっている。
- 環境変数による保存先の変更（Codex の `CODEX_HOME` に当たるもの）は `agy --help` には出てこない。未調査。

## 本文の取得元: `transcript_full.jsonl`

会話の本文を読むなら `brain/<ID>/.system_generated/logs/transcript_full.jsonl` が一番扱いやすい。SQLite の `steps` テーブルと同じステップ列を JSON で書き出したもので、1 行が 1 ステップ。

- `steps` の行数と transcript の行数は、完了済みの会話では一致した（227 / 227）。
- 実行中の会話では SQLite が 19 行、transcript が 18 行で、**実行中のステップ（status 6）は transcript にまだ書かれていなかった**。transcript は完了したステップを追記していくと考えられる。
- `chunks/transcript_full/*.jsonl` を番号順につなげると、`transcript_full.jsonl` とバイト単位で一致した。chunks は使わなくてよい。
- 不正な JSON の行は 0。最長行は `transcript_full.jsonl` で 62,348 bytes、`transcript.jsonl` で 8,568 bytes。

### 共通フィールド

```json
{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-07T00:00:00Z","content":"<USER_REQUEST>\nテストを追加して\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-11-07T09:00:00+09:00.\n</ADDITIONAL_METADATA>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-07T00:00:01Z","input_tokens":7498,"cache_read_tokens":0,"output_tokens":120,"thinking":"...","tool_calls":[{"name":"run_command","args":{"CommandLine":"go test ./...","WaitMsBeforeAsync":5000,"toolSummary":"Run tests"}}]}
{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-07T00:00:03Z","content":"Created At: 2026-11-07T09:00:01+09:00\nCompleted At: 2026-11-07T09:00:03+09:00\nCommand exited with code 0.\n\nCommand output:\nok\n"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-07T00:00:04Z","input_tokens":2248,"cache_read_tokens":8000,"output_tokens":80,"content":"テストを追加しました。"}
```

上記は説明用に作り直した例。

- `step_index` は 0 からの連番。SQLite の `steps.idx` と同じ。
- `created_at` は UTC の RFC 3339（秒精度）。ツール結果の本文中の `Created At` / `Completed At` はローカル時刻（+09:00）で、形式が違う。
- 値のないフィールドは省略される（`thinking`、`tool_calls`、`content`、`error`、`truncated_fields`）。

### type の種類（会話 2 件の合計）

| type | source | 件数 | SQLite の step_type | 内容・扱いの案 |
|---|---|---:|---:|---|
| `USER_INPUT` | `USER_EXPLICIT` | 6 | 14 | 人間の入力。下記のタグを外して `KindUser` |
| `PLANNER_RESPONSE` | `MODEL` | 121 | 15 | モデルの応答。`thinking` / `content` / `tool_calls` |
| `GENERIC` | `MODEL` | 117 | 132 | ツールの実行結果 |
| `SYSTEM_MESSAGE` | `SYSTEM` | 1 | 101 | システム通知。`KindNotice` か無視 |

`status` は `DONE` が大半で、`GENERIC` に `ERROR` が 2 件あった。SQLite の `status` は DONE が 3、ERROR が 7、実行中が 6 だった（数値と名前の対応は観察からの推測）。

#### USER_INPUT: 人間の入力と自動付加

`content` は人間が入力した文字列そのものではなく、XML 風のタグで包まれている。

| タグ | 出現数（会話 1 件・5 入力） | 内容 |
|---|---:|---|
| `<USER_REQUEST>` | 5 | 人間の入力本文 |
| `<ADDITIONAL_METADATA>` | 5 | `The current local time is: ...` |
| `<USER_SETTINGS_CHANGE>` | 1 | モデル選択の変更通知（最初の入力に付いていた） |

- 表示には `<USER_REQUEST>` の中身だけを使う。タグが無い場合に備え、無いときは `content` 全体を使う、くらいの扱いでよさそう。
- 入力本文中にタグと同じ文字列があった場合の扱い（エスケープの有無）は未確認。
- `source` は `USER_EXPLICIT` しか観察していない。名前からすると暗黙の入力（自動で入るもの）の値もありそうだが未確認。
- 画像の添付（`.user_uploaded/` に入ると思われる）は今回は無く未確認。

#### PLANNER_RESPONSE: 応答・推論・ツール呼び出し

- `thinking`: 推論の平文。121 件中 44 件にあった。`"I'm thinking through how to approach this.\n\n\n"` のような定型文だけのこともある。`KindThinking` にできる。
- `content`: アシスタントの本文。`tool_calls` を持たない最終応答にだけあった（会話 1 件で 5 件、人間の入力 5 回と対応）。`KindAssistant`。本文中のファイルへのリンクは `file://` の絶対パス。
- `tool_calls`: `{"name": ..., "args": {...}}` の配列。**呼び出し ID が無い**。
- `input_tokens` / `cache_read_tokens` / `output_tokens` がステップごとにある。

観察したツール名と引数:

| name | 回数 | args のキー |
|---|---:|---|
| `run_command` | 75 | `CommandLine`, `WaitMsBeforeAsync`, `toolSummary` |
| `read_file` | 22 | `Path` |
| `write_file` | 8 | `Path`, `Content`, `Overwrite`（ある場合とない場合がある） |
| `edit_file` | 4 | `Path`, `Target`, `Replacement` |

（会話 1 件・227 ステップの集計。もう 1 件も同様のツールだった。）

#### GENERIC: ツールの結果とその対応付け

ツールの結果は `call_id` で結ばれていない。**`tool_calls` を持つ `PLANNER_RESPONSE` の直後に、呼び出しの数だけ `GENERIC` が同じ順番で続く**という並びになっていた。

```text
U  P(3 calls)  G G G  P(1)  G  P(1)  G  ...  P(content)  U ...
```

- 1 回で 3 つのツールを呼んだ例では、続く 3 つの `GENERIC` の本文が `read_file` → `run_command` → `run_command` の順に対応していた。
- 実装するなら「直前の PLANNER_RESPONSE の未解決の呼び出しを先頭から順に埋める」形にする。並列実行の結果が順不同で返る場合や、途中で中断された場合に崩れる可能性があるが、未確認。
- `content` の先頭は必ず `Created At: <local>\nCompleted At: <local>\n` の 2 行（117 件すべて）。その後にツールごとの本文が続く。
  - `run_command`: `Command exited with code N.\n\nCommand output:\n...`。終了コードはここから取れる。
  - `read_file`: `Showing lines 1-106 of 106\n1: ...` のように行番号付き。
- エラー時は `status: "ERROR"` と `error` フィールドがあり、`content` は `Encountered error in step execution: ...`。`KindTool` のエラーとして扱える。
- 出力が長すぎる場合、`content` は `The tool's output was truncated due to being too long. You can find the full output in a file at file://.../steps/<N>/output.txt.` で始まり、中央を省いたプレビューになる。全文は `steps/<N>/output.txt`（N はこの GENERIC の `step_index`）。`transcript_full.jsonl` でもこの切り詰めは起きる。
- `steps/<N>/output.txt` は切り詰めが起きていないステップにもある（`Created At` の行を除いた本文と思われる）。表示には transcript の `content` を `history.TruncateResult` で切り詰めれば足りるので、`steps/` を読む必要はない。

#### SYSTEM_MESSAGE

`<SYSTEM_MESSAGE>` で包まれた通知。観察したのは「サーバーの再起動でサブエージェントとバックグラウンドタスクが停止した」という 1 件。同じ内容が `messages/<UUID>.json`（`sender`、`priority`、`hideFromUser: true` などを持つ）にもある。`hideFromUser` が true なので、表示しないか `KindNotice` にとどめる。

### `transcript.jsonl`（切り詰め版）との違い

- 長い値を `<truncated N bytes>` で中央省略する。どのフィールドを切ったかを `truncated_fields`（例: `["content"]`）で示す。
- **`tool_calls[].args` の値が JSON エンコードされた文字列になる**。`transcript_full.jsonl` では `"WaitMsBeforeAsync": 5000` なのに、こちらでは `"WaitMsBeforeAsync": "5000"`、`"CommandLine": "\"git status\""` のようになっている。
- 行数・`step_index` は full と同じ。取得元としては full を読み、full が無いときの代替にとどめるのがよさそう。

## 一覧用の情報: タイトル・作業ディレクトリ・時刻

transcript には作業ディレクトリもタイトルも入っていない。別のファイルから取る必要がある。

| 情報 | 取得元の候補 |
|---|---|
| タイトル | `cache/conversation_metadata.json` の `summary.Title`、`annotations/<ID>.pbtxt` の `title:"..."`、`conversation_summaries.db` の `title` |
| 作業ディレクトリ | `summary.WorkspaceURIs`（`file://` の URI 配列）、`conversation_summaries.db` の `workspace_uris`、`history.jsonl` の `workspace` |
| 最初の入力・更新時刻・ステップ数 | `summary.Preview` / `UpdatedAt` / `NumSteps`、または transcript から計算 |

- `cache/conversation_metadata.json` は「キャッシュ」で、**2 件のうち 1 件しか入っていなかった**（調査中に進行していた会話が無い）。これだけを一覧の元にはできない。
- `annotations/<ID>.pbtxt` は 2 件ともあった。protobuf のテキスト形式 1 行（`title:"Gemini Codex Session Files Investigation"`）。タイトルの取得にはこれが手軽。
- `conversation_summaries.db` は 2 件とも入っていた。`conversation_id`、`title`、`preview`、`step_count`、`last_modified_time`、`workspace_uris`、`parent_conversation_id`、`nesting_depth`、`agent_name`、`app_data_dir`、`last_user_input_time` などがあり、一覧の元としては一番揃っている。ただし SQLite への依存が増え、稼働中は直接開けなかった。FTS5 の全文検索索引（`conversations_fts`）も同じファイルにある。
- `history.jsonl` は入力ごとに `display`、`timestamp`（UNIX ミリ秒）、`workspace`（絶対パス）、ある場合は `conversationId` と `type`（`slash_command`）を持つ。**新しい会話の最初の入力には `conversationId` が無かった**ので、これだけでは最初の入力と会話を結べない。
- `cache/last_conversations.json` はワークスペースのパス → 最後の会話 ID の対応だけ。
- 会話の期間は transcript の `created_at` の最小・最大で取れる。
- ブランチを記録している場所は見つからなかった。

今回の実装を作るなら、セッションの列挙は `brain/*/.system_generated/logs/transcript_full.jsonl` を起点にし、タイトルは `annotations/`、作業ディレクトリは `conversation_metadata.json` → `history.jsonl` の順で補う、というのが SQLite を使わない範囲での案。作業ディレクトリが取れない会話が出るのは許容する。

## SQLite: `conversations/<ID>.db`

```text
trajectory_meta(trajectory_id, cascade_id, trajectory_type, source)
steps(idx, step_type, status, has_subtrajectory, metadata, error_details,
      permissions, task_details, render_info, step_payload, step_format)
gen_metadata(idx, data, size)
executor_metadata(idx, data)
parent_references(idx, data)
trajectory_metadata_blob(id, data)
battle_mode_infos(idx, data)
```

- 会話の本体（正）はこちらで、`step_payload` などは protobuf の blob。`.proto` が無いので中身は解読していない。
- `trajectory_meta.trajectory_id` は会話 ID とは別の値だった。`trajectory_type` は 4、`source` は 17（2 件とも）。
- `parent_references` は 0 行、`steps.has_subtrajectory` は確認していない。サブエージェントや分岐を使った会話が無く、親子関係の表現は未調査。
- transcript があれば SQLite は読まなくて済む。transcript が書かれない条件（設定で無効にできるか、古い版で無いか）は未確認。

## 再開コマンド

`agy --help` に以下がある。

```text
--conversation  Resume a previous conversation by ID
--continue, -c  Continue the most recent conversation
```

```sh
cd '/path/to/workspace' && agy --conversation '2e03f28f-1458-420c-a692-3b5ad4780796'
```

- 会話は作業ディレクトリ（ワークスペース）と結び付いているので、そのディレクトリで実行する形にする。別のディレクトリから ID 指定で再開できるかは未確認。
- 対話中の `/resume` も `history.jsonl` に `slash_command` として残っていた。
- **実セッションを再開する操作は実施していない**。`--help` の確認だけ。

## 未対応・不明な点

- 会話 2 件だけの観察。画像入力、サブエージェント（`native_swarm` などの組み込みスキルがある）、分岐、圧縮・要約、中断、plan モード、リモートエージェント（`--remote-agent`）の記録形式は未確認。
- `tool_calls` と `GENERIC` を順序で対応付けるのは推測。並列実行・中断時の並びは未確認。
- `USER_INPUT` の `source` が `USER_EXPLICIT` 以外になる場合、タグの種類の全体像は未確認。
- SQLite の protobuf blob、`implicit/*.pb`、`jetbox_summaries_proto.pb` は解読していない。
- `~/.gemini/antigravity/`（IDE 版と思われる）の形式は手元に無く未調査。
- 保存先を変える環境変数・設定の有無は未調査。
- 公式ドキュメントやソースコードとの照合はしていない。
