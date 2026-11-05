# Codex CLI のセッションファイル（対話履歴）の調査メモ

`apps/agent-history` の Codex 取得元を作るため、ローカルの rollout を読み取り専用で調べた記録。

- 調査日: 2026-11-05。全ファイルの集計時刻は 20:26:32 JST。
- 手元の CLI: `codex-cli 0.166.0`（`codex --version`）。保存データの `cli_version` は下記。
- **公開仕様として互換性を保証された保存フォーマットではない内部形式**として扱う。以下のフィールド・件数は実データの観察結果であり、公式の仕様ではない。
- 調査対象は `$CODEX_HOME`（今回は未設定なので `~/.codex`）の `sessions/`、`archived_sessions/`、`session_index.jsonl`。認証情報や設定ファイルは読んでいない。

## 置き場所

```text
$CODEX_HOME/                         # 未設定時は ~/.codex
  sessions/YYYY/MM/DD/
    rollout-YYYY-MM-DDTHH-MM-SS-<UUID>.jsonl
  archived_sessions/
    rollout-YYYY-MM-DDTHH-MM-SS-<UUID>.jsonl
  session_index.jsonl                # id / thread_name / updated_at
  history.jsonl                      # 別途存在するが今回の取得元では使わない
  state_5.sqlite                     # 別途存在するが今回は開いていない
```

- `-codex-root` には `sessions/` の**親**を指定する。通常・アーカイブの両方を再帰的に探す。
- ファイル名から ID や作業ディレクトリを復元せず、先頭の `session_meta.payload.id` / `cwd` を使う。
- 通常 155 ファイル / 136,194,974 bytes、アーカイブ 1 ファイル / 230,670 bytes。合計 156 ファイル / 136,425,644 bytes。稼働中のセッションもあるため、調査中にサイズ・件数が増える。
- 先頭の `session_meta` が示す `source` は `vscode` が 48、`subagent` オブジェクトが 108。先頭の ID の種類は 156。

## 形式: JSON Lines

1 行 1 JSON オブジェクト。概ね `timestamp`（RFC 3339）、`type`、`payload` の順。`payload` の内容は種類によって変わる。

```json
{"timestamp":"2026-11-05T00:00:00Z","type":"session_meta","payload":{"id":"11111111-0000-0000-0000-000000000001","cwd":"/tmp/demo","cli_version":"0.166.0","source":"cli","git":{"branch":"main"}}}
{"timestamp":"2026-11-05T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"テストを追加して"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.text"]}}}
```

上記は説明用に作り直した例。実データではモデル、指示、ワークスペース、コンテキスト、トークン使用量などのフィールドもある。

- 集計時の最長行は 849,402 bytes。不正 JSON 行は 0。画像などでさらに大きくなることも考え、読み込みは `history.EachLine` を使う。
- 追記中に末尾が途中で切れる場合を考慮し、デコードできない行や未知の種類は飛ばす。I/O エラーは返す。
- `session_meta` はファイルに 1 行とは限らない（後述の継承履歴）。

### トップレベルの種類（全 156 ファイル）

| type | 件数 | 今回の扱い |
|---|---:|---|
| `session_meta` | 176 | 先頭の ID / cwd / git.branch / source |
| `response_item` | 17,650 | 会話・推論・ツールを表示 |
| `event_msg` | 17,261 | 中断のみ通知表示。会話の重複を避ける |
| `turn_context` | 1,231 | cwd が無い場合の補完 |
| `world_state` | 405 | 本文表示には使わない |
| `token_usage_record` | 4,012 | 本文表示には使わない |
| `inter_agent_communication_metadata` | 235 | 本文表示には使わない |
| `compacted` | 5 | message を `KindSummary` にする |

各行の `timestamp` の最小・最大をセッションの期間とする。本文を表示しないレコードの時刻も含める。

`session_meta` の `cli_version` の内訳（継承された meta を含む 176 行）:

| バージョン | 行数 |
|---|---:|
| `0.153.0-alpha.5` | 1 |
| `0.162.0-alpha.17.2` | 30 |
| `0.163.0-alpha.11.1` | 55 |
| `0.164.0` | 1 |
| `0.164.0-alpha.11.1` | 85 |
| `0.166.0` | 4 |

### response_item の種類

| payload.type | 件数 | 表示 |
|---|---:|---|
| `message`（user） | 1,793 | 下記の判定で人間の入力だけ `KindUser` |
| `message`（assistant） | 1,934 | `KindAssistant` |
| `message`（developer） | 800 | 無視 |
| `reasoning` | 4,131 | summary の平文だけ `KindThinking` |
| `function_call` / `function_call_output` | 1,048 / 1,032 | call_id で結合して `KindTool` |
| `custom_tool_call` / `custom_tool_call_output` | 2,915 / 2,914 | 同上 |
| `agent_message` | 960 | エージェント間のメッセージ。`KindNotice` |
| `additional_tools` | 121 | ツール定義。無視 |
| `compaction` | 2 | 今回は無視（トップレベル compacted とは別） |

#### 人間の入力と自動挿入

**`role == "user"` だけでは人間の入力と判定できない**。AGENTS.md、環境情報、スキル、プラグインの案内、アプリ側の状態などもこの role に入る。

観察した user レコードには `internal_chat_message_metadata_passthrough.content_item_kinds` があり、`content` の各ブロックと同じ長さで対応していた。

- `user.text` / `user.image` を人間の入力として扱う。1 レコードに複数のテキストブロックがあるので結合して 1 発言にする。
- `agents_md.instructions`、`environments.environment_context`、`skills.selected_skill_instructions`、`plugins.recommendations`、`additional_content.*`、`generic.turn_aborted` は人間の入力にしない。
- 今回の実装は kinds があれば `user.` で始まるブロックだけ採用する。メタデータが無い形式に備え、`# AGENTS.md instructions`、`<environment_context>`、`<skill>`、`<recommended_plugins>`、`<external_codex_apps_`、`<subagent_notification>` などの既知の自動挿入の接頭辞を除外する。
- メタデータが明示的に `user.text` を指定している場合は、AGENTS.md に似た書き出しでも採用する。
- 画像は `[image]` の文字にする。画像本体や暗号化データは表示しない。

人間の入力のないセッションは一覧に出さない。`event_msg.item_completed` にも `UserMessage` / `AgentMessage` などが入るが、`response_item` と二重に数えたり表示したりしない。

#### アシスタント・推論・ツール

- assistant の `message.content` は `output_text` などのブロック配列。phase による分離はせずファイル順に表示する。
- `reasoning.summary` は `summary_text` の配列。空の場合もある。`encrypted_content` は解読・表示しない。
- `function_call` は `name` と JSON 文字列の `arguments`、`custom_tool_call` は `name` と文字列の `input` を持つ。入力は表示用に 600 bytes までにする。
- 結果の `output` は文字列の場合と `input_text` などのブロック配列の場合がある。`call_id` をキーに既存のツール項目の `Result` を埋め、`history.TruncateResult`（4,000 bytes）で切り詰める。
- エラーは構造化結果の `is_error` / `isError`、または結果中の `Process exited with code N`（N が非 0）で判定する。すべてのツールの失敗を判定できるわけではない。
- 対応する呼び出しのない結果は表示しない。結果未到着の呼び出しは結果が空のまま表示する。

### event_msg の種類

| payload.type | 件数 | 内容・扱い |
|---|---:|---|
| `item_completed` | 9,536 | 会話・ツールなどの UI 用情報。重複するため使わない |
| `token_count` | 4,091 | トークン量。使わない |
| `task_started` / `task_complete` | 1,226 / 1,196 | ターンの開始・終了。使わない |
| `thread_settings_applied` | 1,187 | 設定。使わない |
| `turn_aborted` | 25 | `KindNotice` |

## 作業ディレクトリ・ブランチ・タイトル

- cwd とブランチは先頭の `session_meta.payload.cwd` / `git.branch` から取る。cwd がなければ `turn_context.cwd` で補完する。ブランチが記録されていなければ空にする。
- `session_index.jsonl` は `id`、`thread_name`、`updated_at` の JSONL。今回の実装は同じ ID の最後の空でない `thread_name` をタイトルに使う。索引がない・ID が見つからない場合は最初の人間の入力を `history.OneLine(text, 80)` にする。
- SQLite と索引がどの時点で同期されるかは未調査。SQLite への依存は増やしていない。

## 再開コマンド

`codex resume <SESSION_ID>`。CLI 0.166.0 の `codex resume --help` で UUID / session name を引数に取ることを確認した。[OpenAI 公式の CLI リファレンス](https://learn.chatgpt.com/docs/developer-commands?surface=cli)にも同じ構文がある。

```sh
cd '/tmp/demo' && CODEX_HOME='/tmp/codex-home' codex resume '11111111-0000-0000-0000-000000000001'
```

今回の実装は、取得元が既定の保存先（`$CODEX_HOME` または `~/.codex`）以外のときだけ `CODEX_HOME` を付け、cwd があればそのディレクトリで再開する。パスと ID はシェル用に単一引用符でエスケープする。相対指定した root はコマンド生成時に絶対パスにする。

バージョン・ヘルプの確認時には `CODEX_HOME` を `/private/tmp` の存在しない専用パスに向け、元の `~/.codex` に CLI が書き込まないようにした。**実セッションを再開する操作は実施していない**。アーカイブを直接再開できるか、権限・認証を含む再開の成否は未検証。

## サブエージェント・分岐・圧縮

- サブエージェントも同じ `sessions/YYYY/MM/DD/rollout-*.jsonl` に保存されていた。先頭の `session_meta.source` が `{"subagent":{"other":"guardian"}}` や `{"subagent":{"thread_spawn":{...}}}` になっている。
- `thread_spawn` には `parent_thread_id`、`depth`、`agent_path` などがある。今回はサブエージェントを一覧から除外する。親のファイル中の `agent_message` は補足として残す。
- `forked_from_id` が先頭 meta にあるファイルは 20。これらは子の meta の後に親の meta がもう 1 つある。**先頭だけを所属の判定に使わないと、親の ID や source で上書きしてしまう**。
- `compacted` の `message` は要約として表示するが、`replacement_history` は再表示しない。コピーされた履歴の重複を避けるため。
- 分岐の親子ツリー、巻き戻し、圧縮後の有効履歴の再構築はしていない。ファイルの追記順を表示する。

## 読み込みの性能・実データでの確認

2026-11-05 20:26:32 JST、一時テストで **`codex.New("").Sessions()` を 1 回実行**した。計測区間は New と Sessions、Go のコンパイル時間を含めない。ファイルごとに `runtime.NumCPU()` を上限として並列に読む。

- 読み込み結果: **48 セッション、261 プロンプト**。
- 一覧の読み込み時間: **159.772583 ms**。
- 含まれたセッションのファイルサイズ合計: 69,416,355 bytes（除外するサブエージェントを含む走査量の合計とは別）。
- 続けて 48 セッションすべての `Entries()` もエラーなく読めた。種類別の件数は user 261、assistant 705、thinking 167、tool 2,134、notice 155、summary 0。
- 一時テスト全体の Go 表示は `TestLocalRealData (0.47s)`。一覧読み込み時間と会話の全件読み込みを混同しない。
- キャッシュ状態は制御しておらず、コールド読み込みの測定や複数回平均ではない。一時テストファイルは計測後に削除した。

## 未対応・不明な点

- 古い版や今後の版で metadata がない場合の人間の入力判定は接頭辞による近似。未知の自動挿入を人間の入力と誤認する可能性、接頭辞が同じ正当な入力を除外する可能性がある。
- `event_msg.user_message` しかない形式は手元で確認できず未対応。イベントと response_item の任意の混在形式も保証していない。
- 作業中に追加した入力の `Queued` 判定は未対応。今回は人間の入力として表示し、作業中かどうかは推測しない。
- `response_item.compaction`、暗号化された推論、未知のツール形式、画像の実表示は未対応。
- fork の継承履歴の厳密な重複排除、ロールバック反映、サブエージェントのツリー表示は未対応。
- 索引の時刻順・同期の保証、SQLite の完全なスキーマ・索引との優先順位、アーカイブの再開動作は未調査。
- 今回は公式ドキュメントで再開構文を確認したが、Codex CLI のソースコードとの保存形式の照合は行っていない。

## fixture とテスト

`internal/codex/testdata/home/` は観察した構造に合わせて小さく作り直した架空のデータ。実会話・個人情報・認証情報は入れていない。通常・アーカイブ・自動挿入のみ・親の meta を含むサブエージェントを含めた。

`codex_test.go` では一覧、会話、再開コマンド、CODEX_HOME、保存先なし、I/O エラー、混在した content の分類、64 KB を超える行、結果の切り詰め、途中で切れた末尾を確認する。UI と `history.Source` インターフェースは変更していない。
