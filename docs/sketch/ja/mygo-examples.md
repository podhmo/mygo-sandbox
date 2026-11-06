# mygo の examples を動かして分かった必要事項

[mygo.md](./mygo.md) の調査をもとに `examples/` に Native UI のサンプルを 3 つ書き、テスト・起動・パッケージ化まで試した記録。

- 環境: macOS 15.8.1 (arm64), Go 1.27.3, mygo v0.7.4, Bun なし
- 日付: 2026-11-05

## 追加した examples

| ディレクトリ | 内容 | 試した API |
|---|---|---|
| `examples/counter` | 最小のカウンター | `Column/Row`, `Text`, `Button/PrimaryButton`, `Clicked`, `c.Key` |
| `examples/todo` | Todo リスト | `TextInput` + `Submitted`(Enter), `Checkbox`, `Scroll`, ループ内の `c.Key(id)`, `Disabled`, `Label`, `Theme` |
| `examples/async` | goroutine の結果を反映 | `Window.Update`, `Spinner`, `c.Now()` + `c.After()`（時計） |

それぞれに `main_test.go`（`ui.NewTester` でウィンドウ無しに操作するテスト）がある。

```sh
go test ./...                          # 全テスト（ウィンドウは開かない）
SNAPSHOT=/tmp/todo.png go test ./examples/todo   # 描画結果を PNG で保存
go test -tags e2e ./examples/counter     # 実ウィンドウでの E2E（automation プラグイン。mygo-testing.md）
go run ./examples/counter              # 普通に起動
go tool mygo dev examples/counter      # live reload で開発
go tool mygo build examples/counter    # .app / .dmg を作る
```

## 結果

| 項目 | 結果 |
|---|---|
| `go vet ./...` / `go test ./...` | OK（全 3 パッケージ。テストは 1 パッケージ 0.2〜0.7 秒） |
| `tt.Image()` によるスナップショット | OK。ウィンドウ無しで実際の見た目の PNG が得られる |
| `go run` / `go build` で起動 | OK。メニューバーに File/Edit/View/Window が自動で付く |
| `go tool mygo dev examples/counter` | OK。ウィンドウ表示・クリック・ダークモードへの追従を目視で確認 |
| `go tool mygo build examples/counter` | OK。`examples/counter/dist/darwin-arm64/` に `counter.app` (9.0 MB) と `.dmg` (2.9 MB)。ビルド約 10 秒 |
| `CGO_ENABLED=0 go build` | OK |
| `GOOS=windows` / `GOOS=linux` へのクロスビルド | OK（ビルドのみ。実行はしていない） |
| `go tool mygo doctor` | go / macOS(WKWebView) は ✓。bun / makensis / osslsigncode / codesign は optional で未導入 |

## 洗い出した必要事項・ハマりどころ

### 環境・セットアップ

1. **Go 1.27.3 以上が必須**。mygo の go.mod が `go 1.27.3` なので、利用側の go.mod もそれ以上になる。
2. **Native UI なら Go だけで足りる**。Bun は Web フロント（`mygo-cli`）を使うときだけ必要。
3. CLI は `go get -tool github.com/egoist/mygo/cmd/mygo@<ver>` で go.mod の `tool` にして `go tool mygo` で使うと、ライブラリと CLI のバージョンが揃う。
4. `mygo init -template native` は `.agents/skills/mygo-maintenance/` も生成する（エージェント向けの skill）。このリポジトリは init を使わず手で書いたので含めていない。

### リポジトリ構成

5. **1 つの go.mod に複数のアプリを置ける**。`mygo dev [dir]` / `mygo build [dir]` はディレクトリを引数に取る。
6. **`mygo.json` は無くても動く**。既定値は name=ディレクトリ名、identifier=`com.mygo.<name>`、version=`0.1.0`、出力先=`<dir>/dist/`。配布するなら identifier などを `mygo.json` で明示する必要がある（init が作る雛形は `"out": "build"`）。
7. `.gitignore` に入れるもの: `.mygo/`（dev 用のアプリバンドル。`<dir>/.mygo/dev/...` に `"<name> Dev.app"` ができる）、`dist/`（または `build/`）。

### コードの書き方

8. **ループ内の要素には `c.Key(id)` を付ける**。同じ親の下の同種要素の状態（フォーカス、押下中など）を区別するため。Key が重複するとテスターでは panic になる。
9. ボタンのテキストが同じものが並ぶ場合（"Delete" など）は、`.Label("Delete "+title)` を付けるとテストで `tt.Click("Delete xxx")` のように指定できる（スクリーンリーダー用の名前にもなる）。
10. **別 goroutine から状態を触るときは必ず `win.Update(func(){...})`**。`NewWindow` の戻り値の `*mygo.Window` が要るので、`WhenReady` の中で作ったウィンドウを goroutine に渡す設計にする。
11. テストしやすくするには、goroutine を起動する部分を関数フィールドなどに切り出して差し替えられるようにしておく（`examples/async` の `fetch`）。テストでは view の外で状態を変えたあとに `tt.Frame()` を呼ぶ。
12. `ui.Tester.Find` は `(ui.Rect, bool)` を返す（最初 nil と比較して vet で落ちた）。

### 実行・確認まわり

13. `mygo dev` はリポジトリにコミットが 1 つも無いと `git log` の警告（`fatal: your current branch 'main' does not have any commits yet`）を出すが、ビルド・起動は続行される（バージョン情報の取得に git を使っている模様）。
14. 素の `go build` だとバイナリは約 14 MB、`mygo build` の .app では 9 MB（strip などがされている模様）。
15. **ターミナルから `screencapture` するとウィンドウが写らない**（壁紙とメニューバーだけになる）。ターミナルに画面収録の権限が無いため。自動で見た目を確認したいときは、`ui.NewTester` + `tt.Image()` のスナップショットを使うのが手軽で確実。
16. 配布まで考えると、macOS では Developer ID による署名と公証（codesign）、Windows インストーラーには makensis、Windows の署名には osslsigncode が別途必要（`mygo doctor` で確認できる）。

## 応用: `apps/agent-history`（Claude Code などの履歴ビューア。旧 cc-history）を作って分かったこと

Claude Code の過去のセッションを一覧して読む GUI を mygo で作った（`go run ./apps/agent-history`）。セッションファイルの形式は [claude-code-session-files.md](./claude-code-session-files.md) を参照。

使ったもの:

- `ui.Split` … サイドバーと本文の 2 ペイン。幅は `*float32` で状態に持つ
- `ui.List` + `ui.ListState` … 仮想化リスト。`Selected *int` で行の選択、`Header func(int) bool` でプロジェクト名の見出しを固定表示（見出し行は選択されない）、`Key` で行の同一性、`Label` でタイプして選ぶ・読み上げ
- `ui.SearchField` + `Changed()` … 絞り込み
- `ui/markdown` の `markdown.View(c, text, &opts)` … Claude の返答を Markdown で表示。毎フレーム文字列を渡しても、差分しか解析し直さない
- `ui.Collapsible(c, label, &open, fn)` … ツール呼び出し・thinking・compact の要約を折りたたむ。開閉状態の `bool` は自分で持つ（ここではエントリと同じ長さの `[]bool`）
- `Text(...).Selectable()` … 本文を選んでコピーできるようにする。`Font("monospace")` で等幅フォント
- `mygo.Clipboard.WriteText` … 再開用コマンド（`cd ... && claude --resume <id>`）をコピー

気づいたこと:

- **読み込みは goroutine + `win.Update`** という examples/async の形がそのまま使えた。起動時に全セッションを集計し、セッションを選ぶたびにその会話を読み込む
- 副作用（読み込み・クリップボード）を app の関数フィールドにしておくと、`ui.NewTester` のテストで同期実装に差し替えられる。テスト用のデータは `testdata/` に置いた
- `ListState` には `Changed()` が無い（`List(...)` が返す Element の方にある）。今回は `selected` の値を毎フレーム見て、表示中のセッションと違えば読み込む形にした
- リストの選択行はアクセント色の背景になるので、行内の補足テキストに `TextMuted` を使うと読みにくい。選択中は `t.AccentText` に切り替えた
- 実データ（150 セッション）でも、一覧の構築は 0.25 秒程度、スクロールも引っかからない

## 未検証・次に試したいこと

- Web page 版（Bun を入れて `bunx mygo-cli init`、`mygo.Bind` と生成される TS クライアント）
- `mygo dev -web`（Native UI を WebAssembly でブラウザ実行）
- List（仮想化）/ Table / Router / Markdown / Code editor などの重めのコンポーネント
- メニュー・トレイ・通知などのデスクトップ API
- Windows / Linux での実行
