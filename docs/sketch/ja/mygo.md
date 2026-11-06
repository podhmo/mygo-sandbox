# mygo (MyGo!!!!!) 調査メモ

- 対象: [egoist/mygo](https://github.com/egoist/mygo) / サイト <https://mygo.egoist.dev/>
- 調査バージョン: `github.com/egoist/mygo v0.7.4`（2026-11-05 時点の `@latest`）
- 一次資料: モジュール同梱の `docs/`（サイトの `/docs` はこの markdown から生成されている。`website/README.md` 参照）。
  手元では `go list -m -f '{{.Dir}}' github.com/egoist/mygo` で場所が分かる。
- サイトには LLM 向けの `/llms.txt`・`/llms-full.txt` があり、各ページの URL に `.md` を付けると markdown 版が取れる。
- 読む順の目安: `docs/getting-started.md` → `docs/ui/README.md` → 使う部品の `docs/ui/<部品>.md`（1 部品 1 ページ、プレビュー付き）。`docs/architecture.md` は数千行あり、OS ごとの実装を調べるとき用。書き方の決まりは `ui/doc.go` と、`mygo init` が生成するエージェント向けの手引き（`cmd/mygo/template/shared/.agents/skills/mygo-maintenance/SKILL.md`）が一番まとまっている。

## 一言でいうと

Go でデスクトップアプリを作るフレームワーク。1 つのウィンドウに次のどちらかを表示する（1 つのアプリで混在もできる）。

| 種類 | 中身 | 必要なもの |
|---|---|---|
| **Native UI** | package `ui` で Go だけで書く。MyGo 自身が GPU に描く（macOS: Metal / Windows: Direct3D 11 / Linux: OpenGL）。HTML/JS/webview なし | Go のみ（Linux は GTK 3） |
| **Web page** | OS の webview（WKWebView / WebKitGTK / WebView2）で表示。フロントは Vite + TS。Go のメソッドを `mygo.Bind` で公開すると型付き TS クライアントが生成される | Go + Bun（+ Linux は WebKitGTK 4.1、Win10 は WebView2 Runtime） |

特徴（README から）:

- **Pure Go / cgo なし**（OS API は `ebitengine/purego` 経由）なので、どのマシンからでも全プラットフォーム向けにクロスビルドできる
- 成果物は数 MB の単一バイナリ。メモリ・CPU の消費が少ないことを重視
- デスクトップ API: ウィンドウ、メニュー、トレイ、ダイアログ、通知、グローバルショートカット、ディープリンク、ファイル関連付け、ファイル監視、secrets など
- 配布: .app/.dmg、Windows インストーラー、.deb、PKGBUILD、署名・公証、差分の自動アップデート（Sparkle 風）
- Native UI はブラウザ向けにも動かせる（WebAssembly + WebGL 2。`mygo dev -web`）

対象は **デスクトップ（macOS / Windows / Linux）とブラウザ** だけで、iOS / Android は無い。

## 必要環境

- Go **1.27.3 以上**（`go.mod` の `go 1.27.3`）
- Native UI だけなら Go 以外は不要
- Web フロントを使うなら [Bun](https://bun.sh)（`bunx mygo-cli init`）
- `go tool mygo doctor` で環境チェックができる

## Native UI の書き方

### 基本形

```go
type counter struct{ n int }            // アプリの状態は自分の型で持つ

func (s *counter) view(c *ui.Context) { // フレームごとに呼ばれる view 関数
	ui.Column(c).Fill().Center().Gap(12).Children(func() {
		ui.Text(c, fmt.Sprint(s.n)).FontSize(40).Bold()
		if ui.PrimaryButton(c, "Increment").Clicked() { // 前フレーム以降のイベントを問い合わせる
			s.n++
		}
	})
}

func main() {
	s := &counter{}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{Title: "Counter", Width: 320, Height: 240, Content: ui.View(s.view)})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
```

- **immediate mode** 型の API。view は状態から毎フレーム UI を組み立てる純粋な関数のように書き、`Clicked()` / `Submitted()` / `Changed()` などでイベントを問い合わせる
- 要素はメソッドチェーンでスタイルを付ける（`Fill/Center/Gap/Padding/Grow/Width/FontSize/Bold/TextColor/Disabled/Label ...`）
- 入力系ウィジェットは状態へのポインタを受け取る: `ui.TextInput(c, &s.name)`、`ui.Checkbox(c, &it.done, "label")`
- **`c.Key(k)`** で次の要素に同一性（キー）を与える。ループで並べる要素や同種のボタンが並ぶ場面で必要。同じ親の下で同じ Key を使うと状態が共有されてしまう（テスターでは panic、アプリではログが出るだけ）
- `c.Theme()` でテーマの色（`TextMuted` など）。デスクトップのダーク/ライトに追従する
- `c.Shortcut(mods, key)` でキーボードショートカット

### 別 goroutine からの状態変更

view はメインスレッドで状態を読む。別 goroutine から変えるときは `Window.Update` を使う（メインスレッドで fn を実行して再描画する。待たずに返る）。

```go
win := mygo.NewWindow(mygo.WindowOptions{Content: ui.View(app.view)})
go func() {
	items, err := fetchItems()
	win.Update(func() { app.items, app.err = items, err })
}()
```

- `Window.Invalidate` は再描画だけを要求する（状態を自前でロックしている場合）
- view の中では `c.Invalidate()` で次フレームを、`c.After(d)` で d 後のフレームを要求できる（時計など）

### 書くときの決まり（寿命と同一性）

immediate mode なので「毎回組み立てるだけ」に見えるが、内部では要素の状態（フォーカス、スクロール位置、入力中の文字など）を保っていて、次の決まりがある（SKILL.md と `ui/doc.go` より）。

- **`*ui.Context` と要素（`ui.Element`）を保存しない**。要素は次の組み立てで無効になる（開発中と Tester では panic、本番では何もしない）。持ち越してよいのはアプリのデータ、ウィジェットの状態、`ui.Handle`（`.Bind(&handle)` で要素に結びつける。フォーカスや位置を後から問い合わせる用）。
- **状態を持つ部品には組み立てる前に `c.Key(k)`** で同一性を与える。リストの行は `.ItemKey`。
- **1 つのイベントで view が何回か組み立て直されることがある**。view の中で無条件に副作用（読み込みの開始、ファイル書き込み）を起こさない。`Clicked()` などで条件を付けるか、`OnClick` / `OnChange` を使う。`Changed` / `Submitted` は次の組み立てで見える。
- **別 goroutine に渡してよいのは `c.Services()`**（クリップボード、URL を開く、`Invalidate`）。モデルの更新は `Window.Update` で行う。
- これらは `go tool mygo vet .` が検査する（Go の vet に加えて、組み立て中の値の保持や goroutine へのキャプチャを見る）。

### テスト（ウィンドウ無し）

`ui.NewTester(view, w, h)` でウィンドウ無しに view を動かせる。`go test` だけで UI のテストができるのが大きい。

```go
tt := ui.NewTester(app.view, 320, 240)
tt.Click("Increment")       // テキスト or Label で要素を探してクリック
tt.Type("hello")            // フォーカス中の入力に文字を入力
tt.Key(0, ui.KeyEnter)
tt.HasText("1"); tt.Texts() // 表示内容の確認
tt.Frame()                  // view 外で状態を変えたあとの再描画
tt.Advance(time.Second)     // テスター独自の時計を進める（sleep しない）
img := tt.Image()           // 最後のフレームを *image.RGBA で取れる → スナップショット
```

- 同じテストを実ウィンドウで回す仕組みは無い。実ウィンドウで試すなら automation プラグイン（下の plugins）で別に E2E を書く。
- Tester には `Window.Update` に当たるものが無いので、goroutine を使う読み込みはテストで同期に差し替える。

詳しくは [mygo-testing.md](./mygo-testing.md)（Tester の機能一覧、automation の使い方、スクリーンキャプチャ）。

### コンポーネント

docs/ui/ に 1 コンポーネント 1 ページで揃っている。おおまかに以下がある。

- 基本: Text, Button, Link, Toggle, Checkbox, Switch, Radio, Select, Slider, Stepper
- 入力: TextInput, NumberInput, DecimalInput, SearchField, Combobox, Autocomplete, TokenField, リッチテキストエディタ
- コード: Code editor（Zed 風、マルチカーソル・折りたたみ・補完）, Diff viewer
- コレクション: List（仮想化）, Table, Tree, Outline, GridView
- その他: Markdown 表示（ストリーミング対応）, Calendar/Date/Time input, Color picker, Chart, Dialog/Popover/Toast/Tooltip, Tabs/Sidebar/Split, Router（ページ遷移）, WebView 埋め込み, Video
- 描画系: Canvas 描画, シェーダー（Go 構文）, アニメーション / トランジション / スプリング
- アクセシビリティ（スクリーンリーダー対応）、RTL、i18n、インスペクター

## plugins

`github.com/egoist/mygo/plugins/...` にある公式のもの（`docs/plugins.md`）。Web ページ向けは Go 側を `mygo.Use` で登録し、JS 側を npm から入れる。Native UI 向けはパッケージを import して view の中で使うだけ（`mygo.Use` 不要）。

| plugin | 内容 |
|---|---|
| Automation | 別プロセス（E2E テスト・エージェント）から動いているアプリを操作する Unix ソケット。要素の木、操作、入力、メニュー、PNG キャプチャ。リリースビルドには入らない |
| Terminal | Native UI のターミナル（Ghostty のエミュレータ） |
| Glass | Native UI で macOS の Liquid Glass（どの OS でも） |
| Loading | loading.dev の 29 種のローディング表示 |
| Game | 固定ステップのゲームループ、入力、2D カメラ、ゲームパッド |
| Updater | Sparkle 風の自動アップデート画面（Native UI でも Web でも） |
| Fetch / WebSocket / SQLite / Watch | Web ページ向け。CORS 無しの fetch、ヘッダ付き WebSocket、SQLite（cgo 無し）、ファイル監視 |

## CLI (`mygo`)

`cmd/mygo` が CLI。`go get -tool github.com/egoist/mygo/cmd/mygo` で go.mod の tool として固定し、`go tool mygo ...` で実行するのが推奨。

| コマンド | 内容 |
|---|---|
| `mygo init [-template native] <dir>` | プロジェクト雛形を作る。native なら Go モジュールだけ（main.go / main_test.go / mygo.json / resources/icon.png） |
| `mygo dev [dir]` | 開発用アプリをビルドして起動し、`.go` や設定の変更でリビルド・再起動（live reload）。**再起動なのでアプリの状態は消える**。コンパイルに失敗したら動いている方を残す。ターミナルで `r` + Enter で手動の再ビルド。`-web` でブラウザ版 |
| `mygo build [dir]` | パッケージ化。macOS なら `.app` と `.dmg`。`-platform darwin/universal,windows/amd64,linux/amd64` でクロスビルド。`-debug` でインスペクターなど開発用の機能を残す |
| `mygo generate` | Web フロント用の TS クライアント生成 |
| `mygo doctor` | 環境チェック |
| `mygo vet [dir]` | Go の vet に加えて、組み立て中の値（要素など）の保持や goroutine へのキャプチャを検査する（上の「書くときの決まり」） |
| `mygo migrate-ui [dir]` | mygo のバージョンを上げたときの UI の API の書き換えを表示し、`-write` で適用する |
| `mygo install-skills [dir]` | 同梱のエージェント向け skill（`.agents/skills/mygo-maintenance/`）を入れる・更新する |
| `mygo keygen` | アップデート署名用の鍵生成 |

設定ファイル:

- `mygo.json` か `mygo.config.ts` の **どちらか一方**（両方は不可）。項目は同じ（`name` / `identifier` / `version` / `out` / `macos.infoPlist` など）。
- `mygo init -template native` は `mygo.json`（読むのに JS の実行環境が要らない）、Web の雛形は `mygo.config.ts` を作る。Native UI なら無くても動く（[mygo-examples.md](./mygo-examples.md) の 6）。

### 開発中の道具

- **インスペクター**: 開発ビルドの窓で `F12` / `Alt+Cmd+I`。Chrome の DevTools 風に、要素の木（`Key` や `Label` 付き）、スタイル・ボックス、要素を指して選ぶピッカー、フレームの時間（Performance）、問題（`Key` の重複など）が見られる。`go build` のバイナリにも入る（`-tags mygo_noinspector` で外す）。`mygo build` のリリースでは外れる。
- 遅いフレームのログ: 環境変数 `MYGO_FRAME_STATS`。
- 目安の性能: `docs/ui/performance.md` に Tester（CPU 描画）でのフレーム時間がある。例: 100 万行のリストの見えている行だけで 14.5 µs、50 KB の Markdown 全体で 1.3 ms（Apple M5）。

## Web page 版（今回は未検証）

```go
type Greeter struct{}
// Greet returns a greeting.
func (Greeter) Greet(name string) string { return "Hello, " + name }

func main() {
	mygo.Bind(Greeter{})
	mygo.App.WhenReady(func() { mygo.NewWindow(mygo.WindowOptions{Title: "Hello", URL: "/"}) })
	if err := mygo.App.Run(); err != nil { log.Fatal(err) }
}
```

```ts
import { Greeter } from "./mygo"; // 生成される
document.body.textContent = await Greeter.greet("Ada");
```

- `mygo.NewEvent[T]("name")` で型付きイベントを送れる。チャネルで値をストリームすることもできる
- `bunx mygo-cli init my-app && bun run dev` で始める（Bun が必要）

## 使い分けの目安（公式の説明）

- Native UI: ツール、設定画面、インスペクター、ユーティリティ、すぐ起動してほしいアプリ
- Web page: リッチなドキュメント、既存の Web コードの流用、ブラウザにしかできないこと

## 関連

- 実際に examples を書いて動かした結果: [mygo-examples.md](./mygo-examples.md)
- テストの仕方（実ウィンドウの E2E を含む）とスクリーンキャプチャ: [mygo-testing.md](./mygo-testing.md)
- nsgo との比較: [nsgo.md](./nsgo.md) の「mygo との違い」と「mygo 側の対応状況の確認」
