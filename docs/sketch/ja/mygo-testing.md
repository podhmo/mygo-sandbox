# mygo のテストの仕方とカメラ（nsgo と比べて）

[nsgo.md](./nsgo.md) で nsgo を試したときに「テストを実ウィンドウでも動かせる」のが楽だったので、mygo 側で同じことがどこまでできるかを調べ直した記録。あわせて、カメラ（とマイク）の扱いも調べた。

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

`apps/agent-history`（mygo 版）はコピーを `a.copyText = func(s string){ mygo.Clipboard.WriteText(s) }` という関数フィールドにして、テストで差し替えている。調べると、view からは `c.Services().WriteClipboard(text)` が使え、こちらは Tester では `tt.Clipboard()` に入る。

試した結果（Tester の中でボタンを押して書き込む）:

| 書き込み方 | 結果 |
|---|---|
| `c.Services().WriteClipboard("via services")` | `tt.Clipboard()` が `"via services"` |
| `mygo.Clipboard.WriteText("via global")` | **テストが固まる**（`mygo.onMain` でメインループを待ち続け、`-timeout` で落ちた）。アプリが動いていないので main スレッドの仕事が回らない |

`Services` は窓のサービス（クリップボード、URL を開く、`Invalidate`）を持つ値で、フレームをまたいで保持してよい（skill にも「永続的なクリップボード／URL のコールバックや背景からの再描画には `c.Services()` をキャプチャせよ」とある）。nsgo の `cx.SetClipboard` → `app.Clipboard` と同じ形にできる。ただし非同期の読み込み（`win.Update`）は Tester に対応する手段が無いので、そちらの差し替えは残る（`Services.Invalidate` は任意の goroutine から呼べるが、モデルの更新は `Window.Update` で行うように、とある）。

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

mygo のリポジトリは `internal/e2e/` に実バックエンドを動かすテストを大量に持っている（`MYGO_E2E=1 go test ./internal/e2e`、デスクトップのセッションが要る）。窓・メニュー・ドラッグ・PDF・WebView・カメラなど OS ごとの挙動を確かめるもの。`internal` なので利用側からは import できず、アプリ向けの手段は上の automation プラグイン。

## カメラ（とマイク）

結論: **mygo の Native UI にはカメラから映像を取る API が無い**。カメラは **Web page（WebView）側で `getUserMedia()` を使う** 前提で、Go 側はその許可と状態を管理する。nsgo も同様にカメラの API は持っておらず（`android.permissions` に `CAMERA` などを足せる程度）、こちらは比較対象が無い。

### Web page 側でカメラを使う

- **許可**: ページがカメラ・マイク・位置情報・通知を求めると、既定では **アプリ自身のページ（カスタムスキーム含む）は許可、それ以外は拒否**。`Page.SetPermissionHandler(func(req mygo.PermissionRequest) bool { ... })` で判断を差し替えられる（main スレッドで呼ばれる。`req.Origin`, `req.Permissions` に `mygo.PermissionCamera` など）。
- **OS の許可**: macOS はアプリごとに 1 回ユーザーに聞き、**`Info.plist` に説明が無いとカメラ・マイクを使った瞬間にアプリを終了させる**。Web 版は `mygo.config.ts` の `macos.infoPlist`、Native 版は `mygo.json` の `macos.infoPlist` に `NSCameraUsageDescription` / `NSMicrophoneUsageDescription` を書く。
- **状態の取得と操作**: `page.CaptureState(mygo.CaptureCamera)` が `CaptureNone` / `CaptureActive` / `CaptureMuted` を返し、`page.OnCaptureStateChanged` で変化を受け取れる（録画中のインジケータを窓やメニューバーに出す用途）。`page.SetCaptureState(kind, mygo.CaptureMuted)` でページの協力なしにミュート・停止できる（通話アプリのミュートボタン）。ミュート中のトラックは黒いフレーム・無音になる。種類は `CaptureCamera` / `CaptureMicrophone` / `CaptureDisplay`（`getDisplayMedia()`）。
- プラットフォーム差:
  - macOS: `WKWebView` の `cameraCaptureState` など（macOS 12+）。画面共有は private API（13+）。カメラとマイクのミュートが WebKit 内で連動していて、`SetAudioMuted` で片方が一瞬ミュートされる
  - Linux: WebKitGTK 2.34 以降の `webkit_web_view_get_camera_capture_state`
  - Windows: WebView2 は状態を教えないので **常に `CaptureNone`、操作も何もしない**
- Native UI の窓では `CaptureState` は常に `CaptureNone`。Native UI の中に埋め込んだ `ui.WebView` のページでは使える。

### テストではモックのカメラを使う

mygo の `internal/e2e/capture_test.go` は、本物のカメラではなく **WebKit のモックデバイス** を使っている（`WKPreferences` の private な `_setMockCaptureDevicesEnabled:` と、許可プロンプトを出さない `_setMockCaptureDevicesPromptEnabled:`）。`app://localhost/capture` のページで `getUserMedia({audio: true, video: true})` を呼び、`SetCaptureState` でミュート・停止したときに状態とイベントが期待どおり変わるかを見る。macOS では WebKit がそれでもシステムに問い合わせることがあるので `MYGO_E2E_CAPTURE=1` のときだけ走らせ、Linux では常に走る。このフックも `internal` で、利用側からは使えない。

### その他の「カメラ」

- `ui.Image` は JPEG の EXIF の向き（カメラの回転情報）どおりに正立させて表示する。
- `plugins/game` の `game.Camera` はゲームのワールドを見る 2D の視点（`ui.Transform`）で、デバイスのカメラとは無関係。`examples/blackhole` の `camera.go` もシェーダー描画の視点。
- 動画は `ui.Video` + `ui.VideoPlayer`（GIF などのデコーダの再生）で、カメラのライブ映像を流す口は無い。

## 未検証・次に試したいこと

- `apps/agent-history`（mygo 版）のコピーを `c.Services().WriteClipboard` に置き換えて、`copyText` の差し替えを外す
- agent-history の E2E（読み込みの非同期を差し替えずに、実データ相当の `testdata` で `idle` 待ち）
- Web page 版で実際に `getUserMedia` を使い、`SetPermissionHandler` と `Info.plist` の有無で挙動を見る（Bun が要る）
- インスペクターの Frames タブで agent-history の重さを見る
