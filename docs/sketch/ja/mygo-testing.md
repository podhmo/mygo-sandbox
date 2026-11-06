# mygo のテストの仕方とスクリーンキャプチャ（nsgo と比べて）

[nsgo.md](./nsgo.md) で nsgo を試したときに「テストを実ウィンドウでも動かせる」のが楽だったので、mygo 側で同じことがどこまでできるかを調べ直した記録。あわせて、窓の見た目をどうやって画像にしているか（スクリーンキャプチャ）も調べた。

- 環境: macOS 15.8.1 (arm64), Go 1.27.3, mygo v0.7.4（2026-11-06 時点でもこれが最新）
- 日付: 2026-11-06
- 一次資料: モジュール同梱の `docs/ui/testing.md`, `docs/plugins/automation.md`, `docs/ui/inspector.md`, `docs/windows.md`（Camera and microphone / Permissions）, `internal/e2e/`, `cmd/mygo/template/shared/.agents/skills/mygo-maintenance/SKILL.md`

## まとめ

| | mygo | nsgo |
|---|---|---|
| ウィンドウ無しのテスト | `ui.NewTester(view, w, h)`（`go test` の中、1 テスト数十 ms） | `nsgotest.Start` |
| 同じテストを実ウィンドウで | **できない**。Tester のテストはヘッドレス専用 | `go tool nsgo test -target macos` で同じテストがネイティブ入力で走る |
| 実ウィンドウでのテスト | **automation プラグイン**: `-tags e2e` でビルドしたアプリを別プロセスで起動し、Unix ソケット（改行区切りの JSON）で操作する。テストは自分で書く（クライアントは 60 行程度） | 上と同じ仕組みで済む |
| 要素の探し方 | Tester: 表示文字列か `Label`。automation: アクセシビリティの木の `key`（`c.Key` / `.Key`）か `id` | `text:` / `id:` / `label:` のセレクタ（` >> ` で入れ子） |
| 見た目の確認 | Tester: `tt.Image()`（`*image.RGBA`）。automation: `capture` が PNG を返す。**どちらも画面収録の権限は要らない** | `app.Screenshot("name")`（`.nsgo/test/screenshots/`）。権限は要らない |
| 非同期 | Tester には `Window.Update` が無いので、goroutine を起動する部分を差し替えて同期にする | `cx.Post` のままで、`app.WaitFor` / `WaitUntil` で待てる |
| 時計 | `tt.Advance(d)`（Tester 独自の時計） | `Advance` / `FastForward` |
| クリップボード | `c.Services().WriteClipboard` を使えば `tt.Clipboard()` で読める（後述）。`mygo.Clipboard.WriteText` を Tester の中で呼ぶと **固まる** | `cx.SetClipboard` → `app.Clipboard` |
| 目視の確認 | インスペクター（開発ビルドで `F12` / `Alt+Cmd+I`）。Chrome の DevTools 風に要素の木・スタイル・ボックス・フレーム時間 | — |

nsgo のように「1 つのテストをヘッドレスと実ウィンドウの両方で回す」ことはできないが、**実ウィンドウの E2E 自体は automation プラグインで十分に書ける**。ソケット越しなので別言語のテストランナーやエージェントからも操作できる。

## ウィンドウ無し: `ui.NewTester`

[mygo.md](./mygo.md) / [mygo-examples.md](./mygo-examples.md) に書いた基本（`Click` / `Type` / `Key` / `HasText` / `Frame` / `Advance` / `Image`）以外に、`docs/ui/testing.md` と `ui/headless.go` で次のものが使える。

- ポインタ: `ClickWith(ui.Shift, "Row 3")`, `ClickAt`, `Press` / `Move` / `Release`（ドラッグ）, `Scroll`（トラックパッドの即時スクロール）, `Wheel`（マウスホイールの 1 ノッチ。`Advance` で滑らかに動く）, `Pinch`, `DropFiles(x, y, paths...)`
- キーボード: `TypeKey`（キーと入力文字の組）, `KeyDown` / `KeyUp` / `HoldModifiers`, `Compose`（IME の変換中の文字）, `Command("copy")`（メニューの編集コマンド）, `TextCaret`
- コンテキストメニュー: `RightClick("…")` → `Menu()` で一覧 → `ChooseMenuItem("Move to", "Archive")`
- 窓と環境: `SetSize`, `SetScale`, `SetZoom`, `SetDark`, `SetPreferences`, `SetDirection`（RTL）, `SetLocale`, `SetFocused`
- 読み出し: `Texts`, `Find`（矩形）, `Focused`, `Cursor`, `Clipboard` / `ClipboardData`, `OpenedURLs`（`FailOpenURL` で失敗させる）, `Announcements`（スクリーンリーダーに読ませた文）
- フレーム: 入力と `Frame` は view が求める限り最大 20 フレーム回す。`FramePending()` でまだ再描画を求めているか（アニメーションが止まらない、など）を確かめられる
- `Key` の重複はアプリではログだけだが、**Tester では panic** する

mygo init が生成するエージェント向けの skill（`mygo-maintenance`）も、UI の不具合は `ui.NewTester` で再現し、要素が消えて戻る間の追加フレームやフォーカス・入力・ショートカットまで確かめること、`go tool mygo vet .` と `CGO_ENABLED=0 go test ./...` を回すことを勧めている。

### クリップボードは `c.Services()` 経由にすると差し替えが要らない

`apps/agent-history`（mygo 版）は最初、コピーを `a.copyText = func(s string){ mygo.Clipboard.WriteText(s) }` という関数フィールドにして、テストで差し替えていた。調べると、view からは `c.Services().WriteClipboard(text)` が使え、こちらは Tester では `tt.Clipboard()` に入る。

試した結果（Tester の中でボタンを押して書き込む）:

| 書き込み方 | 結果 |
|---|---|
| `c.Services().WriteClipboard("via services")` | `tt.Clipboard()` が `"via services"` |
| `mygo.Clipboard.WriteText("via global")` | **テストが固まる**（`mygo.onMain` でメインループを待ち続け、`-timeout` で落ちた）。アプリが動いていないので main スレッドの仕事が回らない |

`Services` は窓のサービス（クリップボード、URL を開く、`Invalidate`）を持つ値で、フレームをまたいで保持してよい（skill にも「永続的なクリップボード／URL のコールバックや背景からの再描画には `c.Services()` をキャプチャせよ」とある）。nsgo の `cx.SetClipboard` → `app.Clipboard` と同じ形にできる。ただし非同期の読み込み（`win.Update`）は Tester に対応する手段が無いので、そちらの差し替えは残る（`Services.Invalidate` は任意の goroutine から呼べるが、モデルの更新は `Window.Update` で行うように、とある）。

これを受けて、mygo 版の agent-history を書き換えた。`copyText` フィールドを消し、ボタンとメニューから `c.Services().WriteClipboard` を呼ぶ（`copyMarkdown` は `ui.Services` を引数に取る）。テストは `tt.Clipboard()` で確かめる。

- コピーの呼び出しを外すと、コピーを確かめている 3 つのテスト（`TestViewer`, `TestMasking`, `TestCopyMarkdown`）がすべて落ちることを確かめた。
- `tt.Clipboard()` は最後にコピーした値だけなので、コピーのたびに確かめる。マスクを切ったあとのコピーも、`copyMarkdown` を直接呼ばずに UI（チェックボックス → メニュー）から操作するようにした。
- 実際のアプリでも、automation で `Copy session ID` を押して `pbpaste` で本物のクリップボードに入ることを確かめた。

## 実ウィンドウ: automation プラグイン

`docs/plugins/automation.md` より。別プロセスから動いているアプリを操作するためのプラグインで、E2E テストランナーやエージェント向け。macOS / Linux / Windows で同じプロトコル（Unix ドメインソケット上の改行区切り JSON）。Windows は 10 1803 以降。

### 有効にする

アプリが import したときだけ入り、`MYGO_AUTOMATION` にソケットのパスを渡したときだけ待ち受ける（二重のオプトイン）。リリースに入れないよう build tag で分ける。

```go
//go:build e2e

package main

import _ "github.com/egoist/mygo/plugins/automation"
```

```sh
go build -tags e2e -o app .
MYGO_AUTOMATION=/tmp/app.sock ./app
```

- インスペクターを抜いたビルド（`-tags mygo_noinspector`、`mygo build` のリリースビルド）では import していても無効になり、標準エラーにそう出る。`mygo build -debug` か `MYGO_INSPECTOR=1` なら残る
- ソケットは `0600` で作られ、終了時に消える。持ち主のいない古いソケットは置き換える

### メソッド

| メソッド | 内容 |
|---|---|
| `windows` | 窓の一覧（`id`, `title`, `kind`: `content`=Native UI / `page`=Web, `bounds`, `focused`, `visible`） |
| `tree` | 最後のフレームの要素（**スクリーンリーダーが見るもの**）。`id`, `role`, `key`, `label`, `value`, `bounds`, `states`, `actions` など。レイアウトだけの Row / Column は出ない |
| `act` | 要素に操作する（`press`, `focus`, `setValue`, `insertText`, `selectText`, `increment`, `scrollIntoView`, `expand` …）。要素は `id` か `key` で指定 |
| `input` | OS の入力経路で `click` / `pointerDown` / `wheel` / `key`（`CmdOrCtrl+A` の形）/ `text` を送る |
| `menu` | アプリのメニューを `path` で選ぶ（Dock・トレイ・コンテキストメニューは不可。コンテキストメニューは Tester で試す） |
| `capture` | 窓を PNG（base64、デバイスピクセル）で返す |
| `eval` | Web ページの窓で JS を評価する（Native UI の窓には使えない） |
| `idle` | 窓が落ち着く（view がもうフレームを求めない）まで待つ。`timeout`（ms）を付けないと、動き続けるスピナーなどで永遠に返らない |

`id` はフレームごとに変わりうるので、**テストで探す要素には `Key` を付けておく**のが前提の作り。`key` は `c.Key("x")` でも `.Key("x")` でも入る。

### 試した: `examples/counter/e2e_test.go`

counter に `automation.go`（`//go:build e2e` で import するだけ）と `e2e_test.go`（同じタグ）を足した。テストはアプリを `go build -tags e2e` して起動し、ソケットにつないで操作する。

```sh
go test -tags e2e ./examples/counter                          # 実ウィンドウが開いて閉じる（約 1 秒）
SNAPSHOT=/tmp/counter.png go test -tags e2e ./examples/counter  # capture の PNG を保存
go test ./examples/counter                                    # タグ無しでは従来の Tester のテストだけ
```

確かめたこと: `key` での `act`（press）、`tree` の `label` で表示の確認、`tree` の `bounds` から座標を出して `input` の `click`、`capture` の PNG、`menu` での終了。3 回続けて回しても安定して通った。

分かったこと:

1. **ソケットはウィンドウより先に開く**。つないですぐ `idle` を呼ぶと `no_window: the app has no window` になる。`windows` で `visible` な窓が出るまで待つ。
2. **`act` を続けて送ると押下が失われる**。`inc, inc, dec, inc` を間を空けずに送ると 2 のはずが 1 になった。immediate mode なので、次のフレームまでに来た押下は `Clicked()` の 1 回にまとまる（ドキュメントも「前の結果を見てから次を送るなら `idle` で待て」と書いている）。**`act` ごとに `idle`** を挟んだら通った。
3. メニューのアプリ名は `mygo.json` が無いと実行ファイル名になる（`["counter", "Quit counter"]`。`"Counter"` だと `no menu item`）。
4. `capture` は **画面収録の権限なしで撮れた**（`screencapture` コマンドは権限が無いと壁紙しか写らないのとは違う）。Retina なので 320×220 の窓が 640×384（タイトルバーを除いた内容部分）。
5. Unix ソケットのパスには長さ制限（macOS は 104 バイト）があるので、`t.TempDir()` の深いパスは避けて `os.TempDir()` 直下にした。
6. ビルドが入るので初回は数秒、キャッシュ後は 1 秒ほど。Tester（数十 ms）よりはずっと重いので、普段は Tester、配線の確認だけ E2E という分担になりそう。

### mygo 自身の E2E

mygo のリポジトリは `internal/e2e/` に実バックエンドを動かすテストを大量に持っている（`MYGO_E2E=1 go test ./internal/e2e`、デスクトップのセッションが要る）。窓・メニュー・ドラッグ・PDF・WebView・画面共有など OS ごとの挙動を確かめるもの。`internal` なので利用側からは import できず、アプリ向けの手段は上の automation プラグイン。

## スクリーンキャプチャ（窓の見た目を画像にする）

結論: **mygo は OS の画面キャプチャを使わず、自分で描いたシーンをもう一度描いて PNG にする**。自前で GPU に描いているので、窓の中身は自分で持っている。そのため **画面収録の権限は要らない**。ただし窓が見えていないときは古いフレームになる（下記）。nsgo はネイティブビューに描かせているので、テストでは OS のビューの描画機能（macOS は `cacheDisplayInRect:`、Windows は `PrintWindow` など）で撮っていて、こちらも画面収録の権限は要らない。

### 撮り方の一覧

| 方法 | どこで | 何が撮れるか |
|---|---|---|
| `tt.Image()` | `ui.NewTester` のテスト | 最後のフレーム（`*image.RGBA`）。ウィンドウは開かない。CPU で描く |
| `ui.Render(view, w, h, scale)` | どこでも（テスト・ツール） | view を 1 フレームだけ描いた `*image.RGBA`。状態を持たない 1 枚絵のスナップショット向け（下の Segmented の確認に使った） |
| `win.CapturePage()` | 動いているアプリの中 | Native UI の窓なら **内容をそのまま描き直した PNG**（タイトルバーは含まない）、Web ページの窓ならページのスクリーンショット（WebView の機能） |
| `webView.CapturePage()` | Native UI に埋め込んだ `ui.WebView` | そのページだけ。`Window.CapturePage` では **WebView の部分が穴（空白）になる** |
| automation の `capture` | 別プロセス（E2E） | 中身は `win.CapturePage()`。PNG を base64 で返す |

`win.CapturePage()` の中身（`content.go` の `captureContent`）は、main スレッドで窓の描画先から画素を読み、PNG にエンコードするだけ。ドキュメント（`docs/ui/motion.md`、`docs/architecture.md`）によると:

- `examples/counter` の E2E では、320×220 の窓から 640×384（Retina の 2 倍、タイトルバーを除いた内容）の PNG が取れた。権限の確認ダイアログは出なかった。
- macOS では見えていない窓（隠れている、別の Space にある）は **フレームを描かない**。ドキュメントには「`CapturePage` のときはフレームを組み立てるので今の状態が写る」とあるが、**試すと違った**。counter を起動してアプリを隠し（`osascript` で `set visible ... to false`。`windows` の `visible` が false になる）、automation で `+1` を 3 回押すと:
  - `idle` は毎回 `timeout`（`window 1 still draws after 3s`）
  - `tree` の数字は 0 のまま（押下が処理されていない）
  - `capture` は撮れるが、写るのは **隠す前の 0**
  
  つまり **E2E やキャプチャは窓を見える状態にして行う** 必要がある。テストの途中で別のアプリが前に出て窓を完全に覆ったときも同じことが起きうる（未検証）。

### できないこと・注意

- **画面全体や他のアプリの窓を撮る API は無い**（ScreenCaptureKit / `CGWindowListCreateImage` などは使っていない）。撮れるのは自分の窓の中身だけ。
- Web ページが `getDisplayMedia()` で画面共有を始めることはでき、Go 側は `page.CaptureState(mygo.CaptureDisplay)` / `OnCaptureStateChanged` でその状態を知り、`SetCaptureState` で止められる（macOS は WebKit の private API で 13 以降、Windows の WebView2 は状態が分からない）。この場合の画面収録の権限は OS（WebKit）が求めるはずだが未検証。
- `win.SetContentProtection(true)` は **OS のスクリーンショットや画面収録から窓を隠す**設定。`CapturePage` は自分で描き直すので影響を受けないはず（未検証）。
- ターミナルから `screencapture` を使うと、画面収録の権限が無ければ壁紙しか写らない（[mygo-examples.md](./mygo-examples.md) の 15）。見た目を自動で確かめるなら、上の 3 つ（`tt.Image` / `ui.Render` / `capture`）を使う。

### 参考: デバイスのカメラ・マイク

（写真を撮るような用途は今回は不要だが、調べたので短く残す）Native UI にはカメラの映像を取る API が無い。Web ページの `getUserMedia()` を使い、Go 側は許可（`Page.SetPermissionHandler`。既定はアプリ自身のページだけ許可）と状態（`CaptureState` / `SetCaptureState`）を扱う。macOS では `Info.plist` に `NSCameraUsageDescription` が無いと使った瞬間にアプリが終了させられる（`mygo.json` / `mygo.config.ts` の `macos.infoPlist` に書く）。mygo 自身のテストは WebKit のモックデバイスを使っている（`internal/e2e/capture_test.go`）。

## 未検証・次に試したいこと

- agent-history の E2E（読み込みの非同期を差し替えずに、実データ相当の `testdata` で `idle` 待ち）
- `SetContentProtection(true)` の窓を `CapturePage` で撮れるか、隠れた・最小化した窓を撮れるか
- Web ページの `getDisplayMedia()` で画面共有したときの画面収録の権限の扱い（Bun が要る）
- インスペクターの Frames タブで agent-history の重さを見る
