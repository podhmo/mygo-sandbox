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
| テスト | `ui.NewTester`（ウィンドウ無し）。実ウィンドウは automation プラグインでソケット越しに別途書く（[mygo-testing.md](./mygo-testing.md)） | `nsgotest.Start`（ウィンドウ無し）。さらに `go tool nsgo test -target macos` で **同じテストを実ウィンドウ・ネイティブ入力で** 走らせられる |
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

実装したもの: **元の mygo 版の機能はひととおり**。セッション一覧（プロジェクト＝作業ディレクトリごとの見出し付き、仮想化リスト、クリックと矢印キーで選択）、検索、エージェントでの絞り込み（件数つき、狭いとドロップダウン）、会話を別 goroutine で読み込んで表示、ヘッダーのメタ情報（エージェント・パス・ブランチ・期間・prompt 数・サイズ）、Copy resume command / Copy session ID、Copy as Markdown のメニュー（3 段階）、Tools / Thinking の表示切り替え（既定は非表示）、ツール呼び出し・Thinking・Summary の折りたたみ、作業中に送った発言（`Queued`）の表示、Notice の 1 行表示、会話の読み込みエラーの表示、マスクの切り替え、サイドバーの開閉と幅の変更、パスのホームを `~` に縮める表示。
元に無くて増えたもの: サイドバーの開閉、行ごとのエージェントのバッジ、`nsgo test -target macos` での実ウィンドウのテスト。（いずれも「元の mygo 版のアプリに無かった」もの。mygo でもバッジは `ui.Badge`、実ウィンドウのテストは automation プラグインで作れる。サイドバーの開閉だけは mygo に部品が無い。下の「mygo 側の対応状況の確認」）

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
| `ui.Button` | `ui.Button{Text, Icon: lucide.Copy, Variant: ui.ButtonSecondary, Small: true}`。アイコンは同梱の Lucide |
| `ui.MenuButton(c, "Copy as Markdown", func(m){ m.Item(...).Chosen() })` | `ui.Menu{Trigger: ui.Button{...}, Children: []core.Node{ui.MenuItem{Text, Disabled, OnSelect}}}`。`Trigger` のボタンの `OnPress` / `Disabled` は使われないので、読み込み中に選べなくするのは各 `MenuItem.Disabled` で行う。テストでは `app.Press("id:copy")` で開いて `app.Press("role:menuitem Prompts only")` で選ぶ |
| `ui.Collapsible(c.Key(i), title, &a.open[i], ...)` | `ui.Collapsible{Trigger: []core.Node{ui.Icon(lucide.Wrench, ...), ...}, Open, OnOpenChange, Content}`。行のセルは使い回されるので、開閉状態は行ではなくアプリ側に `map[entries の index]bool` の signal で持つ（セッションを読み込むたびに空にする） |
| `ListState.Header`（見出しの行を選択で飛ばす） | `ui.List` には無いので、`cx.Effect` で選択を見て、見出しの行が選ばれたら動いてきた向きの隣のセッションに `selection.Set` し直す |
| `a.shown`（表示する entries の index を毎回作り直す） | `core.NewMemoFunc` で `entries` と `showTools` / `showThinking` から作る。チェックボックスを切り替えると List の件数と行が更新される |
| `ui.Spinner` | `ui.Spinner{Label}` |
| `win.Update(func(){...})` | `cx.Post(func(){...})`（どの goroutine から呼んでもよい） |
| `mygo.Clipboard.WriteText` | `cx.SetClipboard(text)`。テストでは `app.Clipboard` で中身を見られるので、**差し替え用の関数フィールドが要らない** |

テスト（`main_test.go`）で確かめたこと: 一覧の表示、マスクで一覧のタイトルが伏せられる、検索とエージェントの絞り込みで件数が変わる、行のクリックと矢印キーで会話が切り替わる、Markdown の見出しが出る、コピーした Markdown の中身、マスクのチェックボックスが最初からオン、マスクを外すと本文のメールアドレスが出る、コピーの粒度（Prompts only では返答が入らない）、Tools / Thinking が既定で隠れていて切り替えで出入りする、`Entries` がエラーを返したときにエラーと読めたぶんの両方が出る、ツール・Thinking・Summary が畳まれていて開ける、作業中の発言と Notice の表示、プロジェクト見出しとホームの `~` 表示、メタ情報、ID と再開コマンドのコピー（再開できないエージェントにはボタンが無い）、矢印キーで見出しの行を飛ばす（計 8 テスト、ほかにスクロールのテスト 1 つ）。読み込みは goroutine + `cx.Post` のままで、テスト側は `app.WaitFor("text:3 sessions", 5*time.Second)` で待つだけでよかった（mygo 版では読み込みを同期にする差し替えを入れていた）。

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
12. **種類で形が違う行は `core.Dynamic` で作り直す**。会話の 1 行は User（枠付き）/ アシスタント（Markdown）/ ツール（折りたたみ）/ Notice（1 行）で構造が違う。`…Func` で中身だけ差し替える書き方では書きにくいので、行全体を `core.Dynamic(func(cx){ ... })` にし、`shown.Get()` と `r.Index()` を読んで、そのときの発言から静的な値で組み立てた。表示する発言が変わったときだけ作り直される。サイドバーの見出しとセッションの 2 種類は `core.Show{When: 見出しか}` で出し分けた（`When` が変わったときだけ作り直す）。
13. **テストのセレクタの細かい規則**: `text:` は **部分一致**（`text:hello world` は「Go で hello world」にも当たる）で、`text:"…"` が全体の一致、`text:/…/` は **正規表現**（なので `text:/tmp/demo` は「'e' is not a regular expression flag」で落ちる。`text:"/tmp/demo"` と書く）。`id:entry-1 >> text:"Thinking"` のように ` >> ` で入れ子にでき、ヘッダーの Thinking チェックボックスと折りたたみの Thinking を区別できた。`ui.Collapsible` の `TestID` は外側の箱に付き、押せるのは中のトリガーなので、`app.Press("id:entry-5")` は「no pressable view」になる（トリガーの文字か ` >> ` で指定する）。
14. **リストには `MinHeight: core.Pt(0)` を付ける**（実データで「スクロールするとすぐ先頭に戻される」と言われて気づいた）。`ui.List{Style: core.Style{FlexGrow: 1}}` だけだと、flex の子の既定の最小高さ（CSS の `min-height: auto` と同じく中身の高さ）が効いて、リストが **全行の高さ（200 件で 11548pt）まで伸びる**。リスト自身のスクロール範囲は 0 になり、はみ出した分は外側で切られるだけなので、macOS ではトラックパッドで動かしても弾性スクロールで先頭に跳ね返される。仮想化も実質効かず、全行のセルが作られていたはず。`FlexShrink: 1, MinHeight: core.Pt(0)` を付けたら窓の高さに収まり、リストの中でスクロールするようになった。間に挟んだ `core.View{FlexGrow: 1}` にも同じ指定が要る。`TestLongListsScroll`（`scroll_test.go`）で、200 件の一覧と 400 発言の会話でリストが窓より高くならないことと、スクロール位置が保たれることを確かめている（修正を外すとこのテストは落ちる）。`-target macos` ではネイティブのスクロール入力になるので、移動量は指定どおりにならない（2000 を指定して 1919）。
15. 実行ファイルのサイズは mygo 版 21.6 MB / nsgo 版 24.9 MB（`go build`、どちらもドメイン込み、`base` 版で計測）。`.app` は nsgo が stripped で 16 MB。

### 移行するなら

- 一覧は数千件でも `ui.List`（`core.List` の仮想化）で足りるはず（gallery には 100 万行の `DataTable` デモもある）。会話ビューも `core.List` にして、`EstimatedSize` と実測の補正に任せられる。
- 長い会話の 1 エントリが巨大な Markdown のときの `TextView` の重さは未計測。必要なら `ui.Collapsible` で畳むか、ツール結果は `textview` ではなく等幅の `core.Text` にする。
- プロジェクト見出しをスクロール中も上に残したいなら `core.List` の `StickyHeaderIndicesFunc` が使えそう（`ui.List` には無いので、選択やキー操作を自前で持つことになる。未検証）。
- ドメイン層は UI 非依存なので、mygo 版と nsgo 版を並べて保守できる（今回の構成）。

## mygo 側の対応状況の確認

この文書で mygo を引き合いに出している箇所を、mygo v0.7.4 のドキュメント（モジュール同梱の `docs/`）と実際の動作で確かめ直した（2026-11-06）。「nsgo にはあって mygo には無い」と読める書き方をしていたもののうち、実は mygo にもあったものが多い。テストとスクリーンキャプチャの詳細は [mygo-testing.md](./mygo-testing.md)。

| 箇所 | この文書での書き方 | mygo の実際 |
|---|---|---|
| 描画方式の表の下 | 見た目が OS 標準（テキスト選択、IME、アクセシビリティ）になるのが nsgo の利点 | mygo も **自前で実装している**: テキスト選択（Markdown はブロックをまたいで選べる）、IME の変換（`tt.Compose` でテストもできる）、VoiceOver / Orca / Narrator 向けのアクセシビリティの木。違うのは「OS の部品そのものか、OS らしく作り直したものか」 |
| 同上 | GPU は `Shader`（Go 構文 → MSL/GLSL/HLSL）など一部だけで使う | mygo にも Go 構文のシェーダー（`ui/shader`）がある。mygo は画面全体も GPU で描く |
| テスト（表） | mygo は `ui.NewTester` だけ | **実ウィンドウの E2E は automation プラグインで書ける**（`examples/counter/e2e_test.go`）。ただし Tester のテストをそのまま実ウィンドウで回すことはできず、別に書く |
| 事前準備 | mygo は「Go さえあれば良い（Linux は GTK3）」 | 正しい。Linux は GTK 3 と WebKitGTK 4.1（Native UI だけなら配布物の依存から WebKitGTK を外せる: `linux.webview: false`） |
| 事前準備（`.icon`） | nsgo は Xcode の actool が無いと `dev` も `build` も失敗 | mygo も `macos.icon` の Icon Composer `.icon` は **Xcode 26 以降の actool** でコンパイルするが、無いときは **メッセージを出してスキップし、PNG から作った `.icns` のまま続ける**。`mygo dev` はそもそも `.icns` で表示する。止まらない点が nsgo と違う |
| 事前準備（画面収録の権限） | `app.Screenshot` は権限なしで撮れる | mygo の `tt.Image` / `ui.Render` / `win.CapturePage` / automation の `capture` も権限なしで撮れる。mygo は自分で描き直すので OS の画面キャプチャを使わない |
| 対応表: `ui.Split` → `SidebarShell` | 開閉ボタン・幅変更・ヘッダーまで付く | mygo の `ui.Split` は幅変更（ドラッグと矢印キー）と、窓が狭いときに最初のペインが縮む動きまで。**サイドバーの開閉ボタンを持つ外枠の部品は無い**（`ui.Sidebar` は Finder のようなソースリストで、別もの）。開閉は自分で分岐して作る |
| 対応表: `ui.List` | nsgo の `ui.List` で矢印キー・Home/End・ページキーの移動が付く | mygo の `ui.List` も `Selected` を渡すと **↑↓・Home/End（Page Up/Down は Linux と Windows だけ）** で選べ、`Label` で頭文字を打って選ぶこともできる。`Selection[K]` で複数選択（key で保持）もある。選択の変化は List の要素の `Changed()` |
| 対応表: `ListState.Header` | `ui.List` には無いので Effect で飛ばす | mygo は **見出しの行を上に固定（sticky）** し、選択でも飛ばす。「移行するなら」で未検証とした sticky な見出しは mygo では最初から使えていた |
| 対応表: `ui.Segmented`（件数） | nsgo は `Option.Count` で件数を出せる | mygo の `ui.Segmented` は文字列のラベルだけで件数の欄は無い（ラベルに含めるか、`ui.SegmentedBase` で自作する） |
| 書いてみて 11（Segmented が狭いと重なる） | nsgo では等幅に縮んで文字が重なった | mygo では **重ならないが、はみ出した後ろのセグメントが切れて見えなくなる**。`ui.Render` で「All 812 / Claude Code 420 / Codex 211 / Devin 97 / OpenCode 84」を幅 600 と 240 で描いて確かめた（幅 240 では 3 つ目の途中で切れた）。セグメントは中身の幅で、等幅ではない。mygo 版の agent-history も狭いサイドバーでは同じ問題を抱えている（ドロップダウンの `ui.Select` はある） |
| 対応表: `markdown.View` → `ui.TextView` | nsgo はコードのハイライト・ストリーミング表示まである | mygo の `ui/markdown` も **コードブロックのハイライト**（コードエディタと同じトークナイザ）と **ストリーミング**（`markdown.Stream`、トークンを一定の速さで見せる）、ブロックをまたいだ選択がある |
| 対応表: `ui.Badge` / アイコン | nsgo は Badge と同梱の Lucide | mygo にも `ui.Badge` がある。アイコンは `ui.Icon` に SVG を渡す形で、**アイコン集は同梱しない**（Lucide などの SVG を embed する） |
| 対応表: クリップボード | `cx.SetClipboard` なら差し替え不要（mygo は関数フィールドで差し替えた） | mygo も view から **`c.Services().WriteClipboard`** を使えば `tt.Clipboard()` で読め、差し替えが要らない。`mygo.Clipboard.WriteText` を Tester の中で呼ぶとテストが固まる |
| テスト / 書いてみて 1（非同期のままテスト） | mygo 版は読み込みを同期に差し替えた | これは **mygo では本当にできない**。Tester には `Window.Update` に当たるものが無く、`WaitFor` のような待ち合わせも無い |
| 書いてみて 3, 13（セレクタ） | nsgo は `text:` 部分一致・`"…"` 完全一致・正規表現・` >> ` の入れ子・`id:` | mygo の Tester は `Click` / `Find` が **表示文字列か `Label` の完全一致**、`HasText` が部分一致だけ。正規表現・入れ子・key での指定は無い（key で探せるのは automation の `act` / `tree`） |
| 書いてみて 5, 6（行の使い回し、index の範囲外） | nsgo の List の行は使い回される | mygo は immediate mode で毎フレーム行を組み立てるので、この種の問題は起きない（同一性は `Key` で与える） |
| 書いてみて 7（`SetTheme`） | ルートでテーマを設定しないと色が抜ける | mygo のテーマ（`c.Theme()`）は既定で OS のライト / ダークに追従し、設定は要らない |
| 書いてみて 9（`nsgo check`） | Go だけのアプリでは失敗 | mygo の `go tool mygo vet` は Go だけのアプリでそのまま通る（`apps/agent-history`、`examples/counter` で確認。ビルド値の保持や goroutine へのキャプチャを調べる） |
| 書いてみて 14（List が伸びて中でスクロールしない） | flex の最小高さで List が全行の高さまで伸びた | mygo 版の agent-history（`ui.Split` + `ui.List`）では起きていない（実データでスクロールも問題なかった）。mygo にも `MinHeight` / `Shrink` はあるが、指定しなくて済んだ |
| 未検証: `-fps` / `-trace` | — | mygo はインスペクター（開発ビルドで F12 / Alt+Cmd+I）の Performance に各フレームの build / layout などの時間が出る。`MYGO_FRAME_STATS` で遅いフレームをログに出せる |
| 未検証: hot reload | nsgo は TSX だけの変更ならその場で差し替え | mygo の `mygo dev` は Go や設定の変更で **作り直して再起動**（状態は消える）。`r` で手動の再ビルド |
| 未検証: `-target web` / TSX | — | mygo は `mygo dev -web` で Native UI を WebAssembly + WebGL 2 でブラウザに出せる。TSX に相当するのは Web page 版（Vite + TS、`mygo.Bind` から型付きの TS クライアントを生成。Bun が要る） |

この文書の記述で mygo について **誤っていたのは無く**、「nsgo の利点」として挙げたもののうち多くが mygo にもあった、というのが主な訂正点。逆に mygo に無い・弱いことが確かめられたのは、サイドバー開閉の外枠、Segmented の件数欄と狭いときの見え方、Tester での非同期の待ち合わせと柔軟なセレクタ、同じテストを実ウィンドウで回す仕組み、の 4 つ。

## 未検証・次に試したいこと

- 実データ（数千セッション）での起動時間・スクロールの体感（`-fps` / `-trace`）
- `nsgo dev` の hot reload（Go の変更は再ビルド・再起動、TSX だけの変更はその場で差し替え）
- Linux（GTK4）・Windows での実行、`-target web` での実行
- TSX で UI を書き、ドメインを `tsx.Register` / `tsx.RegisterFunctions` で Go から渡す形
