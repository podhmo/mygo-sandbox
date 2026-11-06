# nsgo の調査と agent-history の試作

[NSGo](https://github.com/oneo-me/nsgo)（サイト: <https://oneo-me.github.io/nsgo/>）を調べ、examples を動かしたうえで `apps/agent-history` の UI を nsgo で書き直す試作（`apps/agent-history/nsgo/`）を作った記録。mygo との比較は [mygo.md](./mygo.md) / [mygo-examples.md](./mygo-examples.md) を前提にしている。

- 環境: macOS 15.8.1 (arm64), Go 1.27.3, **Xcode 本体なし（Command Line Tools のみ）**, Bun なし
- nsgo: `main` の 88b0f10（2026-11-06）。リリース・タグはまだ無いので pseudo-version で入れた
- 日付: 2026-11-06

## nsgo とは

- Go で書くクロスプラットフォームの UI エンジン。Rust 製の [Native Shell (NSH)](https://nsh.dev) の設計を Go に移植したもの（コードの移植ではなく設計の移植。NSH の最新リリースを参照実装として追いかけている）。
- **リアクティブなグラフ（signal / memo / effect）、shadow tree、スタイル、レイアウト（Taffy の移植。flexbox / grid）、テキストの組版準備は Go 側の engine goroutine が持ち**、コミットごとに「マウント命令の束（MountBatch）」を作ってプラットフォームのバックエンドに送る。バックエンドはそれを **OS のネイティブビュー** に適用する。
- 対象: macOS / iOS / Windows / Linux / Android / Web（wasm）。アプリを既存の iOS・Android・デスクトップアプリや Web ページに埋め込むこともできる。
- UI は Go の構造体リテラルで書く（`core.View{Children: ...}`）か、**TSX** で書く（goja 上で動く。Node 不要、esbuild を Go から使う）。どちらも Tailwind のクラス（`Class: "flex-row gap-2 p-4"`）が使える。
- 部品の層: `core`（要素・スタイル・リアクティブ）→ `base`（**見た目を持たない**部品: Button, Checkbox, Input, Tabs, Dialog, ResizablePanelGroup, DataTable, TextView, Editor, DiffView, Terminal…）→ `ui`（Onevra という見た目を付けた部品）。
- まだ初期段階。「リリースも安定 API もまだ無い」と README に明記されている。それでいてコード量は Go だけで約 35 万行あり、gallery は NSH の gallery を Go に移植した大きなもの。

### mygo との違い（描画方式）

| | mygo (Native UI) | nsgo |
|---|---|---|
| 描画 | **自前で GPU に描く**（Metal / D3D11 / OpenGL）。即時モード風 (`ui.Text(c, ...)` を毎フレーム呼ぶ) | **OS のネイティブビューに描かせる**（macOS は NSView/CALayer + CoreText、Windows は Direct2D/DirectWrite、Linux は GTK4/Pango/Cairo、Web は DOM）。保持モード + signal |
| OS への到達 | purego（cgo なし） | macOS / Linux も purego、Windows は syscall + COM。macOS は `CGO_ENABLED=0` で動く。iOS だけ Xcode のリンカのため cgo |
| UI の書き方 | `func view(c *ui.Context)` が毎回呼ばれる。状態は普通の Go の変数 | component は **1 回だけ** 実行される。状態は `core.Signal`、変わる部分は `ContentFunc` / `ClassFunc` / `StyleFunc` などの関数で渡す（SolidJS 風） |
| 別 goroutine から | `win.Update(func(){...})` | `cx.Post(func(){...})` |
| テスト | `ui.NewTester`（ウィンドウ無し） | `nsgotest.Start`（ウィンドウ無し）。さらに `go tool nsgo test -target macos` で **同じテストを実ウィンドウ・ネイティブ入力で** 走らせられる |
| 設定ファイル | `mygo.json`（無くてもよい） | `nsgo.yaml`（`dev` / `build` には **必須**） |

GPU は「画面全体を自前で描く」用途ではなく、`Shader`（Go 構文のシェーダーを MSL/GLSL/HLSL に変換）や Canvas など一部の要素だけで使う。見た目が OS 標準（テキスト選択、IME、スクロールの慣性、アクセシビリティ）になるのが利点で、そのぶん各 OS のバックエンドとその依存が要る。

## 事前準備（必要なもの）

mygo は「Go さえあれば良い（Linux は GTK3）」だったが、nsgo は **macOS だけなら同じくらい楽、それ以外とアイコン周りで差が出る**。

| 用途 | 必要なもの | 試した結果 |
|---|---|---|
| ライブラリ・CLI 全般 | Go **1.26** 以上（nsgo の go.mod が `go 1.26.0`）。`go get -tool github.com/oneo-me/nsgo/cmd/nsgo@<commit>` | このリポジトリは 1.27.3 なので問題なし |
| macOS で動かす・テストする | Go だけ。`CGO_ENABLED=0` で可。**Command Line Tools で足りる** | OK（gallery / tray / tsx / 試作） |
| macOS 向け `.app` を作る（PNG アイコン or アイコン無し） | Go だけ（PNG は nsgo が Go で `.icns` にする） | OK |
| **Icon Composer の `.icon` アイコン** | **Xcode 本体の `actool`**。`nsgo build` だけでなく **`nsgo dev` も失敗する** | gallery が `the icon needs Xcode's actool` で落ちた。`assets/AppIcon*.icon` を退避して `Icon.png` に落ちるようにしたら動いた |
| iOS（実機・シミュレータ） | Xcode 本体。実機は Apple ID・Developer Mode | 未検証 |
| Windows | Go だけ（WebView を使うなら WebView2） | クロスビルドのみ OK |
| Linux | **GTK 4.14 以上・Pango・Cairo**（purego で dlopen するので cgo は不要）。WebView は WebKitGTK 6.0、動画は GStreamer | クロスビルドのみ OK（`CGO_ENABLED=0` でも通る）。mygo は GTK 3 だったので要求が上がる |
| Android | 実機・エミュレータに入れるには `adb`。`android_x64` には NDK（cgo が要る） | 未検証 |
| Web | ブラウザ。`nsgo test -target web` は Chrome / Chromium / Edge | 未検証 |
| TSX のアプリ | 追加の準備なし（Node / Bun 不要） | `examples/tsx` は Go だけで動いた |
| Web サイト（`website/`）の開発 | Bun 1.4 以上 | 不要なので入れていない |

その他:

- **画面収録の権限は要らない**: `nsgotest` の `app.Screenshot("name")` は `-target macos` で実ウィンドウの画像を `.nsgo/test/screenshots/macos/` に保存し、これは権限なしで撮れた。逆に `screencapture` コマンドは権限がないと壁紙しか写らず、ウィンドウが出ていないように見えて紛らわしかった（実際にはちゃんと表示されていた）。
- 初回の `go build ./...`（nsgo のリポジトリ全体）は約 23 秒。goja / esbuild / chroma / goldmark などを取ってくる。
- 公式サイトは JS で描画される Astro サイトで、静的に取得すると本文が取れない。リポジトリの `website/src/content/docs/` は空（`.gitkeep` だけ）で、実質的なドキュメントは **リポジトリの `ARCHITECTURE.md` と `docs/architecture/*.md`**（core, scrolling, textview, testing, tool, desktop まわりなど 25 本）と README。

## examples を試した結果

nsgo のリポジトリを `/tmp` に clone して試した。

| 試したこと | 結果 |
|---|---|
| `go build ./...` | OK（約 23 秒） |
| `go test ./examples/...` | OK。gallery 17.5 秒、tsx / embedded 0.4 秒 |
| `CGO_ENABLED=0 go build ./examples/gallery`, `./examples/tray` | OK（gallery 40 MB, tray 16 MB） |
| `go tool nsgo build -target macos_arm64 examples/tsx` | OK。4.7 秒、`dist/TSX-0.1.0-macos_arm64/TSX.app` 22 MB |
| `go tool nsgo test -target macos examples/tsx` | OK。5 テストを実ウィンドウで実行、計 10 秒 |
| `go tool nsgo dev examples/gallery` / `build` | **NG**: `.icon` のために Xcode の actool が要る（上記）。`.icon` を退避すると build は 1.6 秒（キャッシュ後）で `Gallery.app` 33 MB ができ、起動して Stocks などのデモが表示された |
| `go build` したバイナリを直接実行 | 動くが、`resources/` を「実行ファイルの `../Resources`」から探すので、gallery ではフォント読み込みの警告が出る。素の `go run` よりも `nsgo dev` / `nsgo build` で動かすもの |

examples の中身:

- `gallery`: NSH の gallery の Go 版。要素・`base`・`ui` の全デモ（Stocks、チャット、エディタ、ターミナル、DataTable…）。ヘッドレスのテストが大量にある。書き方を調べるときの一番の参考資料。
- `tsx`: TSX + Tailwind で書いたタスク・タイマーのアプリ。`compiler.App(embedFS, "App.tsx")` を `nsgo.Run` に渡すだけ。`*.test.tsx` も Go のテストとして走る。
- `tray`: Dock に出ないメニューバー常駐アプリ。`nsgo.Run(App)` に「ウィンドウを開く関数」を渡す形（`desktop.OpenWindow` / `OpenPanel` / `NewTray` / `RegisterHotKey`）。複数ウィンドウで signal を共有できる。
- `embedded`, `embed-*`: 既存アプリへの埋め込み（デスクトップの Go アプリ、iOS、Android、Web）。

## agent-history を nsgo で書いてみる

### 方針

`apps/agent-history` は UI（`main.go`, mygo）とドメイン（`internal/history`, `redact`, `claudecode` / `codex` / `devin` / `opencode`）が分かれているので、**ドメインはそのまま、UI だけ nsgo で書き直す**。

- Go の `internal` の規則で `apps/agent-history/internal/...` は `apps/agent-history/` 配下からしか import できない。そのため試作は `apps/agent-history/nsgo/`（別の main パッケージ）に置いた。
- 同じ go.mod に mygo と nsgo が同居できるか → **できた**。purego が v0.11.1 → v0.11.2 に上がるが、mygo 側の既存テストも通る。`go get -tool` で CLI を登録すると goja / esbuild などが indirect に増える。

### 試作（`apps/agent-history/nsgo/`）

```sh
go run ./apps/agent-history/nsgo                       # 実データで起動
go test ./apps/agent-history/nsgo                      # ヘッドレス（0.3 秒）
go tool nsgo test -target macos apps/agent-history/nsgo  # 実ウィンドウで同じテスト + スクリーンショット
go tool nsgo build -target macos_arm64 apps/agent-history/nsgo  # .app (16 MB, 4 秒)
```

最初は `base`（見た目なし）と Tailwind クラスで組み、そのあと **`ui` パッケージ（Onevra のテーマ）の部品に置き換えて見た目を整えた**。

実装したもの（元のアプリの一部）: セッション一覧（仮想化リスト、クリックと矢印キーで選択）、検索、エージェントでの絞り込み（件数つき）、セッションを選ぶと会話を別 goroutine で読み込んで表示、本文は Markdown を `ui.TextView` で表示、発言の種類をバッジで表示、マスクの切り替え（一覧・本文とも）、会話を Markdown にしてクリップボードへコピー、サイドバーの開閉と幅の変更。
実装していないもの: プロジェクト見出し、Tools / Thinking の表示切り替え、折りたたみ、コピーの粒度メニュー、Resume コマンド。

対応表（右が `ui` 版で使ったもの）:

| 元 (mygo) | nsgo での書き方 |
|---|---|
| `app` 構造体のフィールド + 毎フレーム描画 | `core.NewSignal` / `NewSignalFunc`（slice は比較できないので常に通知する equal を渡す）、絞り込みは `core.NewMemoFunc` |
| `ui.Split(&w, ...)` | `ui.SidebarShell{Sidebar, SidebarFooter, Header, Actions, Content, Open, Width, ...}`。サイドバーの開閉ボタン、ドラッグでの幅変更、ヘッダーまで付く。gallery と同じ外枠 |
| `ui.List` + `ui.ListState`（`Selected *int`） | `ui.List{Selection: ui.NewSelection(cx, false), Key, Row, RowLabel}`。選択中の行の色、**矢印キー・Home/End・ページキーでの移動** が付く。選択は key で持つ。選択の変化は `cx.Effect(func(){ selection.Keys() ... })` で拾って会話を読み込む |
| `ui.SearchField(c, &a.query)` | `ui.Input{Input: base.Input{Value: query.Get, OnChange: query.Set, Placeholder}}`（`base.Input` を包む） |
| `ui.Segmented`（エージェント選択） | 幅が足りるときは `ui.Segmented[string]{Options: []ui.Option[string]{{Value, Label, Count}}, Value, OnChange, Small, Compact}`（`Count` で件数を横に出せる）、狭いときは `ui.Select[string]` に切り替える（下記 11） |
| `ui.Checkbox` | `ui.Checkbox{Text, Checked, OnChange}` |
| `markdown.View` | `ui.TextView{MarkdownFunc, Selectable: true}`（`textview.TextView` に ui の文字・色を当てたもの）。GitHub 風の Markdown、コードのハイライト（`highlight.New(cx)`）、ストリーミング表示まである |
| 種類の表示 | `ui.Badge{TextFunc, ToneFunc}`（`ToneAccent` / `ToneInfo` / `ToneDanger` …） |
| `ui.Button` / `ui.MenuButton` | `ui.Button{Text, Icon: lucide.Copy, Variant: ui.ButtonSecondary, Small: true}`。アイコンは同梱の Lucide |
| `ui.Spinner` | `ui.Spinner{Label}` |
| `win.Update(func(){...})` | `cx.Post(func(){...})`（どの goroutine から呼んでもよい） |
| `mygo.Clipboard.WriteText` | `cx.SetClipboard(text)`。テストでは `app.Clipboard` で中身を見られるので、**差し替え用の関数フィールドが要らない** |

テスト（`main_test.go`）で確かめたこと: 一覧の表示、マスクで一覧のタイトルが伏せられる、検索とエージェントの絞り込みで件数が変わる、行のクリックと矢印キーで会話が切り替わる、Markdown の見出しが出る、コピーした Markdown の中身、マスクのチェックボックスが最初からオン、マスクを外すと本文のメールアドレスが出る。読み込みは goroutine + `cx.Post` のままで、テスト側は `app.WaitFor("text:3 sessions", 5*time.Second)` で待つだけでよかった（mygo 版では読み込みを同期にする差し替えを入れていた）。

### 書いてみて分かったこと

1. **非同期のままテストできる**。`nsgotest` が engine の Post を回してくれるので、`WaitFor` / `WaitUntil` で待てばよい。時計も `Advance` / `FastForward` で進められる。
2. **同じテストを実ウィンドウでも走らせられる** のが強い。`TestMain(m) { nsgotest.Main(m) }` を置くだけで `nsgo test -target macos` が使え、ネイティブ入力でクリック・入力して通った。`app.Screenshot("name")` で見た目の確認もできる（`.nsgo/test/screenshots/macos/`、`.gitignore` に `.nsgo/` を足した）。
3. セレクタは `text:表示文字列` / `id:TestID` / `label:読み上げ名`（`ui.List` の行は `RowLabel` が label になる）。表示文字列はマスク後のものになるので、テストでは伏せた後の文字列を探す必要があった。キー操作を試すときは `nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop})` でデスクトップを指定する。
4. 保持モードなので、**「どこが変わるか」を `…Func` で明示する** 書き方に慣れが要る。`Content` と `ContentFunc`、`Class` と `ClassFunc`、`Text` と `TextFunc` のように静的・動的の 2 種類のフィールドがある。分岐は `core.Show{When, Then, Else}`、作り直したい部分は `core.Dynamic`。
5. `core.List` / `ui.List` の行は再利用されるので、行の中で `filtered.Get()[i]` を component 実行時に 1 回読んで変数に入れると、スクロールで別の行になったとき表示が古いまま残る。必ず関数の中で読む。
6. **`Key` / `Row` の中の index は範囲外になりうる**。絞り込みで件数が減った直後、行の束縛（選択中かどうか）が古い index で `Key` を呼び、`index out of range` で panic した。`Key` でも範囲を確かめる。
7. **`ui` の部品を使うならルートで `cx.SetTheme(ui.Theme())` を呼ぶ**。呼ばなくても大半はそれらしく描かれるが、一部の色が 0（透明）になり、チェックボックスがオンでも塗られない、サイドバーの地色が無い、などになる（ui のテストのヘルパーがこれを呼んでいて気づいた）。
8. `base` だけで組むと、チェックボックスの印のような細部まで自分で作ることになる。`ui` に置き換えたら Tailwind クラスはほぼ不要になり、色は `ui.ColorsOf(cx)`、文字は `ui.Textf(fn, size, weight, colors.Muted)` で揃えられる。
9. `nsgo check` は Go だけのアプリでは失敗した（TSX の型を生成するために `tsx` パッケージを足して `go list` するので、go.mod に無い依存を要求される）。`vet` までは通る。Go だけのアプリでは使わなくてよい。
11. **`ui.Segmented` は狭いと文字が重なる**。セグメントは等幅に縮み、はみ出しも折り返しもしないので、実データ（All + 4 エージェント、3 桁の件数）を狭いサイドバーで見ると「Claude Code」「Codex」などのラベルと件数が重なって読めなくなった。対処として、セグメントのラベルは短い `AssistantName`（Claude / Codex / Devin / OpenCode）にし、`core.Show{When: func() bool { return sidebarWidth.Get() >= needed() }}` で幅が足りないときは `ui.Select`（ドロップダウン）に切り替えた。必要な幅は「一番長いセグメント × 個数」で見積もる（等幅なので合計ではない）。サイドバーの幅は `SidebarShell` の `Width` / `OnWidthChange` で signal に入っているので、ドラッグ中にしきい値をまたぐとその場で切り替わる。`ui.Select` は `Option.Count` を使わず `Label` も固定の文字列なので、件数入りのラベルは `core.Dynamic` の中で作り直している。
12. 実行ファイルのサイズは mygo 版 21.6 MB / nsgo 版 24.9 MB（`go build`、どちらもドメイン込み、`base` 版で計測）。`.app` は nsgo が stripped で 16 MB。

### 移行するなら

- 一覧は数千件でも `ui.List`（`core.List` の仮想化）で足りるはず（gallery には 100 万行の `DataTable` デモもある）。会話ビューも `core.List` にして、`EstimatedSize` と実測の補正に任せられる。
- 長い会話の 1 エントリが巨大な Markdown のときの `TextView` の重さは未計測。必要なら `ui.Collapsible` で畳むか、ツール結果は `textview` ではなく等幅の `core.Text` にする。
- プロジェクト見出しは `core.List` の `StickyHeaderIndicesFunc` で、コピーの粒度は `ui.Menu` 系で作れそう（未検証）。
- ドメイン層は UI 非依存なので、mygo 版と nsgo 版を並べて保守できる（今回の構成）。

## 未検証・次に試したいこと

- 実データ（数千セッション）での起動時間・スクロールの体感（`-fps` / `-trace`）
- `nsgo dev` の hot reload（Go の変更は再ビルド・再起動、TSX だけの変更はその場で差し替え）
- Linux（GTK4）・Windows での実行、`-target web` での実行
- TSX で UI を書き、ドメインを `tsx.Register` / `tsx.RegisterFunctions` で Go から渡す形
