# mygo (MyGo!!!!!) 調査メモ

- 対象: [egoist/mygo](https://github.com/egoist/mygo) / サイト <https://mygo.egoist.dev/>
- 調査バージョン: `github.com/egoist/mygo v0.7.4`（2026-11-05 時点の `@latest`）
- 一次資料: モジュール同梱の `docs/`（サイトの `/docs` はこの markdown から生成されている。`website/README.md` 参照）。
  手元では `go list -m -f '{{.Dir}}' github.com/egoist/mygo` で場所が分かる。
- サイトには LLM 向けの `/llms.txt`・`/llms-full.txt` があり、各ページの URL に `.md` を付けると markdown 版が取れる。

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

ビルドしたアプリを外から E2E で操作したい場合は automation プラグインがある（試した結果は [mygo-testing.md](./mygo-testing.md)）。

### コンポーネント

docs/ui/ に 1 コンポーネント 1 ページで揃っている。おおまかに以下がある。

- 基本: Text, Button, Link, Toggle, Checkbox, Switch, Radio, Select, Slider, Stepper
- 入力: TextInput, NumberInput, DecimalInput, SearchField, Combobox, Autocomplete, TokenField, リッチテキストエディタ
- コード: Code editor（Zed 風、マルチカーソル・折りたたみ・補完）, Diff viewer
- コレクション: List（仮想化）, Table, Tree, Outline, GridView
- その他: Markdown 表示（ストリーミング対応）, Calendar/Date/Time input, Color picker, Chart, Dialog/Popover/Toast/Tooltip, Tabs/Sidebar/Split, Router（ページ遷移）, WebView 埋め込み, Video
- 描画系: Canvas 描画, シェーダー（Go 構文）, アニメーション / トランジション / スプリング
- アクセシビリティ（スクリーンリーダー対応）、RTL、i18n、インスペクター

## CLI (`mygo`)

`cmd/mygo` が CLI。`go get -tool github.com/egoist/mygo/cmd/mygo` で go.mod の tool として固定し、`go tool mygo ...` で実行するのが推奨。

| コマンド | 内容 |
|---|---|
| `mygo init [-template native] <dir>` | プロジェクト雛形を作る。native なら Go モジュールだけ（main.go / main_test.go / mygo.json / resources/icon.png） |
| `mygo dev [dir]` | 開発用アプリをビルドして起動し、`.go` や設定の変更でリビルド・再起動（live reload）。`-web` でブラウザ版 |
| `mygo build [dir]` | パッケージ化。macOS なら `.app` と `.dmg`。`-platform darwin/universal,windows/amd64,linux/amd64` でクロスビルド |
| `mygo generate` | Web フロント用の TS クライアント生成 |
| `mygo doctor` | 環境チェック |
| `mygo vet` | ビルド値を struct などに保持していないかチェック |
| `mygo keygen` | アップデート署名用の鍵生成 |

設定ファイル:

- native: `mygo.json`（`name` / `identifier` / `version` / `out` など）
- web: `mygo.config.ts`

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
- テストの仕方（実ウィンドウの E2E を含む）とカメラ: [mygo-testing.md](./mygo-testing.md)
