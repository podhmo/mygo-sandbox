# guigui の調査と agent-history の試作

[guigui](https://github.com/guigui-gui/guigui)（Ebitengine 上の GUI フレームワーク）を調べ、examples を動かしたうえで `apps/agent-history` の UI を guigui で書き直す試作（`apps/agent-history/guigui/`）を作った記録。mygo / nsgo との比較は [mygo.md](./mygo.md) / [nsgo.md](./nsgo.md) を前提にしている。

- 環境: WSL2 (Ubuntu 26.04) + **WSLg**（`DISPLAY=:0`、ウィンドウが実際に出る）、Go 1.27.3
- guigui: `main` の 185e409e（2026-11-05）。`v0.2.0-alpha` の pseudo-version で入れた（アルファで API は変わりうる、と README に明記あり）
- Ebitengine: `v2.11.0-alpha.0.20261105142445`
- 日付: 2026-11-07
- 一次資料: リポジトリの README、`doc.go`（環境変数とデバッグ機能）、`skills/using-guigui/SKILL.md`（942 行。ライフサイクルの決まりがかなり詳しい）、`example/` 以下のコード

## guigui とは

- Ebitengine の作者（hajimehoshi 氏）が作っている、Go の GUI フレームワーク。2026 年に `guigui-gui` organization へ移った。
- **"immediate-mode-*inspired*"** と自称する。即時モードの書き味だが、widget は毎フレーム捨てられるのではなく **保持される Go 構造体**（`guigui.DefaultWidget` を埋め込み、zero value で使える）。その代わり、子の木と見た目は `Build` の再実行で組み直される —— Compose / SwiftUI が view 関数を再実行するのと同じ発想。
- 描画は Ebitengine（GPU）。OS のネイティブ部品は使わず、全部自前で描く。テキストは go-text/typesetting で組版し、Hi-DPI と i18n（ロケールごとの字形など）に対応。CJK のフォントは `basicwidget/cjkfont` を import すると同梱フォントで表示される（試作でも日本語が出た）。
- **cgo なしでビルドできる**。この環境でも `CGO_ENABLED=0 go build ./example/counter` が通った。mygo（purego）や nsgo（macOS では purego）と同じく、OS への到達は purego / dlopen 系。Linux の X11 dev ヘッダすら要らない（実行時には X11/Wayland のディスプレイは要る。WSLg で動いた）。
- 対象は Ebitengine が動くところ全部: macOS / Windows / Linux / BSD / WebAssembly / (Ebitengine 経由で mobile も理論上)。

### mygo / nsgo との違い（構造）

| | mygo (Native UI) | nsgo | guigui |
|---|---|---|---|
| 描画 | 自前で GPU（Metal/D3D11/GL） | OS のネイティブビュー | **Ebitengine（GPU）で自前描画** |
| UI の書き方 | `func view(c *ui.Context)` を毎フレーム呼ぶ。状態は普通の Go 変数 | component は 1 回だけ実行。`core.Signal` + `…Func`（SolidJS 風） | widget は保持される構造体。**`Build`（子の組み立て）→ `Layout`（配置）→ 入力 → `Tick`** が 1 tick（60/sec）。`Draw` はフレームごと |
| 状態変更 → 画面 | イベントは `Clicked()` などを次の組み立てで問い合わせる。毎フレーム組み立てるので自動で反映 | signal の通知で `…Func` が再評価される | **ハンドラ内の変更は自動で rebuild**。ハンドラ外の変更は `WriteStateKey`（ハッシュ比較）か `RequestRebuild()` が要る |
| 別 goroutine から | `win.Update(func(){...})` | `cx.Post(func(){...})` | **専用の経路は無い**。チャネル + `Tick` で受けて `RequestRebuild()`（試作はこの形） |
| テスト | `ui.NewTester`（ウィンドウ無し）+ automation プラグインで実ウィンドウ E2E | `nsgotest`（ウィンドウ無し）+ 同じテストを `nsgo test -target` で実ウィンドウ | **ウィジェット単体のテスト基盤は無い**。アプリ丸ごとなら Ebitengine の `exp/vmhost` でヘッドレスに tick 駆動・入力注入・フレーム取得ができる（下記） |
| 設定ファイル・CLI | `mygo.json`（無くてもよい）+ `mygo dev/build/vet` | `nsgo.yaml` 必須 + `nsgo dev/build/test` | **無し**。`go run` / `go build` するだけ（`guigui.Run(root, &guigui.RunOptions{Title, WindowSize, ...})`） |
| 依存 | Go のみ（Linux は GTK3） | macOS は Go のみ、Linux は GTK4 | **Go のみ**。ランタイムにディスプレイ（X11/Wayland）が要る |

「状態をハンドラ内で変えれば自動で組み立て直される」点は mygo の immediate mode に一番近いが、ツリーが保持される分、nsgo 寄りの「同一性・蓄積状態」を意識する場面がある（後述の落とし穴）。

## 事前準備

- Go だけ。Linux の X11 dev ヘッダや GTK も要らない（ebiten は purego でライブラリを読む）。ディスプレイが無い CI でも後述の vmhost で動作確認できる。
- 実行には表示先が要る。WSL2 + WSLg ではそのままウィンドウが出た。
- guigui 自身に CLI・雛形・設定ファイルは無い。`go get github.com/guigui-gui/guigui` して `guigui.Run` を呼ぶだけ。
- エージェント向けの導線が手厚い: リポジトリの `skills/using-guigui/SKILL.md`（widget のライフサイクルと落とし穴）、Ebitengine 側の `skills/run-ebitengine-app-headless`（ヘッドレス駆動）。今回はこの 2 本を読んでから書いた。

## examples を試した結果

`/tmp/guigui` に clone して試した。全部 `go run` で動く（追加の準備なし）。

| 試したこと | 結果 |
|---|---|
| `CGO_ENABLED=0 go build ./example/counter` | OK。cgo なしで通る |
| `go run ./example/counter`（実ウィンドウ） | OK。WSLg 上にウィンドウが出て動いた |
| `example/gallery`, `example/todo` を vmhost で撮影 | OK。gallery はサイドバー + Settings 画面、todo は入力欄 + Create ボタンが描画された |

`example/` には counter, todo, gallery, biglist（10 万件・行高が可変のリスト）, calc, codeeditor（JetBrains Mono 同梱のコードエディタ）, drawer, ebitengine（**別プロセスの Ebitengine アプリを widget として内包する `ebitenginewidget` のデモ**。vmhost guest を子ウィジェットにする変わった部品）, expander, font, gridlayout, imageclipboard, menubar, panels, richtexteditor, texteditor, toast がある。

## ヘッドレスでの動作確認（exp/vmhost）

guigui のアプリは `ebiten.Game` なので、Ebitengine の `run-ebitengine-app-headless` skill がそのまま使える。これがこの環境での主な検証手段になった。

- guest（検証対象）を `-tags ebitenginevmguest` でビルドすると、`ebiten.RunGame` がウィンドウを開く代わりに `EBITENGINE_VM_ENDPOINT` のソケットへ描画命令を流す。host（自分で書く小さいドライバ、`exp/vmhost.GuestSession`）は隠しウィンドウの ebiten アプリで、guest の tick を任意回数進め（`AdvanceTicks`、実時間より速く回せる）、入力を注入し（`MoveCursor`/`PressMouseButton`/`PressKey`/`ScrollWheel`/touch/gamepad）、最終フレームを `CompositeFrame` + `ReadPixels` で PNG にできる。
- ドライバの雛形は ebiten のモジュール内 `skills/run-ebitengine-app-headless/_driver/main.go`。コピーして使う前提のテンプレート。今回は「guest にフラグを引き継ぐ（`exec.Command(guestBin, flag.Args()...)`）」「tick を小分けに進めて間に実時間の sleep を挟む（非同期ロード完了を待つため）」「クリック列と `snapshot(i)` で複数状態を撮る」改造をした。
- 注意点:
  - **host は隠しウィンドウとはいえ描画コンテキストが要る**（WSLg で動いた。ディスプレイ無しの CI では Xvfb などが要る）。guest 側は完全に headless。
  - host と guest の ebiten バージョンは一致必須（同じ go.mod からビルドすればよい）。
  - **`TypeRune` で文字を打っても TextInput には届かない**（テキスト入力は `exp/textinput` の IME セッション経由）。文字列入力の検証は `GUIGUI_DEBUG=emulateclipboard` + `GUIGUI_DEBUG_CLIPBOARD_TEXT` で貼り付けにする、と skill に書いてある。
  - タイミングを揃えたいときは `GUIGUI_COLOR_MODE` / `GUIGUI_KEY_BINDING_MODE` / `GUIGUI_DEBUG=devicescale=1` で固定する。
  - `GUIGUI_DEBUG=showbuildlogs,showinputlogs` で「なぜ rebuild されたか」「どの widget が入力を食ったか」のログが出る。

## 書き方の要点

`skills/using-guigui/SKILL.md` に詳しい決まりがある。試作を書くうえで効いた点:

- widget は `guigui.DefaultWidget` を埋め込んだ構造体。**zero value で使えること**（子はコンストラクタで new せず、フィールドに置いて `&w.child` を渡す）。
- `Build(context, adder) error`: この回で使う子だけを `adder.AddWidget` してから、各子に状態を setter で流し込む + `On…` ハンドラを（毎回）登録する。bounds はまだ不明。idempotent に書く。
- `Layout(context, widgetBounds, layouter)`: `LinearLayout` で子を配置するだけ。`FixedSize` / `FlexibleSize` / 0（= intrinsic の `Measure`）。items スライスは `slices.Delete` で使い回す。
- `Measure(context, constraints)`: 自身の希望サイズ。`Panel` の中身やリスト行の高さに効く。`guigui.FixedWidthConstraints(w)` で幅固定の計測。
- `WriteStateKey(context, writer)`: ハンドラ外で変わる状態をハッシュに書くと、変化を検出して全体 rebuild してくれる（`RequestRebuild()` 不要）。描画だけ変わるものは key に書かず `RequestRedraw(w)`。
- `Env`: 祖先が `Env(context, key, source)` で返す値を子孫が `context.Env(self, key)` で読む DI。共有モデルを渡すのに使う（`EnvKey` は `guigui.GenerateEnvKey()`）。
- `EventKey` + `guigui.SetEventHandler` / `DispatchEvent`: 子→親への通知。
- `guigui.WidgetSlice[*T]`: 可変個の子のアドレスを安定させるスライス（`SetLen` + `At`/`All`）。`[]T` を append すると widget が移動して panic/同一性喪失になるので必須。
- `context.SetEnabled/SetVisible/SetFocused/SetOpacity/SetPassthrough/SetButtonInputReceptive`: Build から呼ぶ widget ごとの属性。

### basicwidget の部品

Background, Button, Text, TextInput, NumberInput, Checkbox, Toggle, RadioButton, Slider, Select[T] / Combobox[T], SegmentedControl[T], List[T]（仮想化。`ListStyleSidebar`、見出し行 `Header`、`IndentLevel`、行の expander、並べ替え、カスタム `Content`）, Table[T], Panel（スクロール。`PanelStyleSide` はサイドバー用の面+枠）, Popup, PopupMenu, ContextMenuArea[T], MenuBar, Form（ラベル+入力の行）, Image, Divider, Expander（折りたたみ、アニメーション付き）, ProgressBar, TooltipArea, Drawer。

`Text` がかなり高機能: `SetMultiline` / `SetSelectable` / `SetEditable` / `SetWrapMode`（単語/任意位置/折り返さない）/ `SetEllipsisString` / `SetBaseStyle` と `SetOverrideStylesInRange`（`TextStyle`: 太字・斜体・weight・色・背景色・下線・言語・フォントファミリ・スケール）/ `TextRange` の hotspot（文字列中のクリック可能領域）/ IME。

無い部品（mygo/nsgo と比べて）: **Markdown 表示**、コードエディタの既成品（example に自前実装はある）、Badge、アイコン集、サイドバー開閉つきの外枠、検索フィールド（`TextInput` + placeholder で書く）、スピナー（自作するか example を見る）、toast 用の既定部品（example/toast はある）。

## agent-history を guigui で書いてみる

mygo 版・nsgo 版と同じく、ドメイン（`internal/history`, `redact`, 各エージェント）はそのまま、UI だけ `apps/agent-history/guigui/main.go` に書き直した（約 1000 行、1 ファイル）。同じ go.mod に mygo / nsgo / guigui / ebiten が同居できた（purego が v0.11.2→v0.11.3 に上がるが、既存テストは全部通る）。

```sh
go run ./apps/agent-history/guigui                     # 実データで起動
go run ./apps/agent-history/guigui -claude-root DIR …  # 保存先の指定は mygo 版と同じフラグ
```

実装したもの（試作の範囲）: セッション一覧（プロジェクト見出し `Header` + 2 行のカスタム行、仮想化リスト）、検索、エージェント絞り込み（`Select`）、マスクの切り替え、会話を goroutine で読み込んで表示、ヘッダー（タイトル・エージェント/パス/ブランチ/期間/prompt 数/サイズの 1 行）、Copy resume command / Copy session ID / Copy as Markdown（`Select` をメニュー代わりに）、Tools / Thinking の件数つき切り替え（既定は非表示）、ツール・Thinking・Summary の `Expander` 折りたたみ、作業中の発言（`You (while working)`）、Notice の中央寄せ 1 行、エラー表示、パスの `~` 縮約、ダークモード（`GUIGUI_COLOR_MODE=dark` で確認）。

試作でやっていないもの: セッション一覧のエージェント色ドット、会話本文の Markdown レンダリング（**`main.go` を作ります。** が生のまま出る）、ツール入出力の等幅フォント、ユーザー発言の色つき枠、サイドバーの開閉・幅変更、sticky なプロジェクト見出し。

対応表（右が guigui で使ったもの）:

| 元 (mygo) | guigui での書き方 |
|---|---|
| `app` 構造体 + 毎フレーム描画 | `model` 構造体。`Root.Env` で `envKeyModel` 経由で配る。ハンドラ内の変更は自動 rebuild、goroutine の結果は `pending chan func(*model)` に入れて `Root.Tick` で適用 + `RequestRebuild()` |
| `ui.Split`（幅変更つき 2 ペイン） | `LinearLayout` 水平（サイドバー `FixedSize(10u)` + 会話 `FlexibleSize(1)`）。サイドバーは `Panel` + `PanelStyleSide` + `Borders{End:true}` |
| `ui.List` + `ListState`（`Header` 行、`Selected`、Key、タイプ選択） | `basicwidget.List[int]`。`ListItem.Header` の見出し行がそのままある。`SetItems` + `OnItemSelected`（index 返し）。選択の復元は `SelectItemByIndex`。複数選択・並べ替え・`KeyText` もあるが未使用 |
| セッション行（2 行の組み合わせ） | `ListItem.Content` に自作 `sessionRowWidget`（`Text` 2 個）。**行の色は自分で塗る決まり**（下記「書いてみて」3） |
| `ui.SearchField` | `basicwidget.TextInput` + `SetPlaceholder` + `OnValueChanged`（毎文字で絞り込み） |
| `ui.Select`（エージェント） | `basicwidget.Select[string]`。**`SetItemsByStrings` は Value を埋めない**ので `SetItems` で `{Text, Value}` を渡す（下記 1） |
| `ui.Checkbox` | `basicwidget.Checkbox`。**ラベルは無い**ので `Text` を横に置く（文字を押しても反応しない） |
| `markdown.View` | 部品が無い。`Text` + `SetMultiline` + `SetSelectable` のプレーンテキストで代用 |
| `ui.Collapsible` | `basicwidget.Expander`（`SetHeaderWidget`/`SetContentWidget`/`SetExpanded`/`OnExpansionChanged`。開閉がアニメーションする） |
| `ui.MenuButton`（3 択メニュー） | `basicwidget.Select[int]` をメニュー代わりに。先頭に見出し項目を置き、選ばれたら処理して選択を先頭に戻す（下記 5） |
| `ui.Spinner` / ローディング | 無いので状態文の `Text` |
| クリップボード | `clipboard.Write(clipboard.Contents{Text: …})`（Text/HTML/PNG の 3 形式対応） |
| `win.Update`（goroutine → UI） | 上記の `pending` チャネル方式 |
| 絞り込みの件数・種類色・トースト | `TextStyle`（`SetScale`/`SetColor`/`SetBold`/`SetItalic`）+ `basicwidget.TextColorFromTint(context, AccentTintColor()/InfoTintColor()/…)` で色を揃える |

### 書いてみて分かったこと（落とし穴）

1. **`Select.SetItemsByStrings` は `Value` を埋めない**。`SelectItemByValue` は `Value` 一致なので、文字列だけ渡すと何も選ばれずボタンが空欄になる。`SetItems([]SelectItem[T]{{Text, Value}})` と両方立てる（一覧のヘッダ画像で気づいた）。
2. **`SetVisible(false)` でも領域は取られる**。会話の「空き状態の文字」と「会話パネル」を両方 `FlexibleSize(1)` で並べると、片方が見えないのに半分の高さを食い、中身が下に押し出された。**非表示でもレイアウトから外れない**ので、置くかどうかを `Layout` 側でも同じ条件で決める（`Env` は `Layout` でも自分の親をたどって読める）。もしくは `Build` で「使う子だけ `AddWidget`」する決まりに沿って根本的に切り替える。
3. **リスト行のカスタム `Content` は色を自分で塗る**。`Text` だけ置くと選択行の強調背景の上で既定色のまま読めなくなる。行の状態は `context.Env(w, basicwidget.EnvKeyListItemColorType)` を **`Layout`（または `Draw`）で**読み、`ListItemColorType.TextColor(context)` を `TextStyle` に入れる（取得は `Build` ではなく `Layout` で、という決まり。`example/biglist` と同じ形）。取得は必ず `(nil, false)` の可能性を見て comma-ok で。
4. **`WidgetSlice` は `SetLen` しないと `At` が panic**（`index out of range`）。一覧の行を `[]sessionRowWidget` ではなく `WidgetSlice[*sessionRowWidget]` で持ち、Build のたびに `SetLen(len(rows))` → `At(i)` で使う。
5. **メニュー代わりの `Select`** は「先頭に見出し用の選択肢」を置き、実選択肢が選ばれたら処理して `SelectItemByIndex(0)` に戻す形で作れる。ただし **`Unselectable: true` の項目は `SelectItemByIndex` でも選べない**（空欄になる）ので、見出し項目は普通に選べる項目にしておき、ハンドラ側で `Value < 0` を無視する。
6. **`Checkbox` にラベル文字は無い**。`Text` を横に置くパターン（gallery もそうしている）。文字を押しても切り替わらないのが惜しい。
7. **goroutine → UI の専用経路が無い**。mygo の `win.Update` / nsgo の `cx.Post` に相当するものが見当たらず、`RequestRebuild()` も単にフラグを立てるだけ（別 goroutine からの呼び出しは規約上想定されていなさそう）。今回は「`chan func(*model)` に結果を送り、`Root.Tick` で drain して適用 → `RequestRebuild()`」にした。`Tick` はメインの update 上で走るので安全で、待ち時間も最悪 1 tick。`WriteStateKey` に世代カウンタを書く方式でもよいが、更新を検出する側も値の変更をメイン上で起こす必要があり、結果は同じになる。
8. **`Measure` は「コンテンツの高さ」を親が聞くためのもの**。`Panel` の中身 + `FixedWidthConstraints` で「この幅なら何 px か」を各 entryView に聞いて `FixedSize(h)` で縦に積む、という形で会話を組んだ。`LinearLayout` と `Measure` を共有する `layout()` ヘルパーのイディオム（SKILL 推奨）で `Layout` と `Measure` のずれを防げる。
9. **`Build` の中で `context.Env` は自分（`w`）起点で解決する**。まだ誰にも add されていない widget 起点では `(nil,false)` が返る（SKILL 記載のとおり）。親の `Build` で新規作成した直後の子起点では引けないので、モデルは「`Build` が走っている widget 自身」を起点に引く。
10. **`basicwidget.Text` はエディタ寄りの作り**で、`SetSelectable` / `SetEditable` / hotspot（`TextRange` に `OnHotspotDown` でクリック処理）まである。`TextInput` は単一行向けの薄いラッパー。検索欄は `OnValueChanged`（未 commit の変更でも来る）で毎文字絞り込める。
11. **`List` の選択は「クリック」だけでなく**、ドキュメント上はキーボード移動・`KeyText` でのタイプ選択・並べ替え（`OnItemsMoved`）・行ごとの expander（`OnItemExpanderToggled`）まで組み込みである（試作では未使用）。
12. **実行ファイルのサイズ**: `go build` で 48.6 MB（ドメイン込み、cjkfont 入り）。mygo 版 21.7 MB の倍以上ある。cjkfont の同梱フォントが大きいのが主因と思われる（外せばかなり小さくなるはずだが未検証）。

### mygo / nsgo との比較所感

- **書き味は mygo に近い**（「状態を変えれば組み立て直される」）が、ツリーが保持される分「widget の同一性」「蓄積状態のリセット」「`WriteStateKey` での変更検出」という nsgo 寄りの概念も出てくる。ライフサイクルの決まり（`Build` で bounds を読まない、`Layout` で設定しない、ハンドラは毎回登録）は SKILL.md に網羅されていて、それに沿えば素直に書けた。
- **不足しているものの筆頭は Markdown 表示**（agent-history のような本文が多いビューアでは結構効く）。`Text` のリッチスタイル（`SetOverrideStylesInRange`）と hotspot を組み合わせれば自前で組める余地はあるが、mygo の `markdown.View` / nsgo の `ui.TextView` のような一発の部品は無い。
- **ウィンドウ・メニュー・トレイなど「デスクトップの殻」は素の Ebitengine 側**の機能になる（`ebiten.SetWindowTitle` 等）。mygo のようなアプリ層 API（トレイ、通知、ダイアログ、更新配布）は無く、`guigui.Run` も `RunOptions{Title, WindowSize, WindowMinSize, WindowMaxSize, WindowFloating, AppScale, ApplePressAndHoldDelegated, RunGameOptions}` の薄いラッパー。ファイルダイアログは別の `hajimehoshi/dialog` パッケージが go.mod に入っている。
- **ツール類は最小**: `dev` / `build` / `vet` / インスペクター相当は無い（`internal/vettool` は errcheck の multichecker をまとめただけ）。その代わり `go run` で完結し、デバッグは `GUIGUI_DEBUG` のログ系と vmhost のスクショ/入力で回す。
- **vmhost はこの用途にちょうどよい**: ウィンドウ無しで入力列 + スクリーンショットが撮れ、実時間より速く tick を回せる。E2E の仕組みとしては mygo の automation プラグインや nsgo の `nsgo test -target` に相当するが、「別プロセスのアプリを guest として駆動する」構造なので、アプリ側の改修は一切要らない。
- **性能**: 一覧は仮想化 `List` で問題なさそう（`example/biglist` は 10 万件を普通に動かす）。会話側は試作では「発言ごとに widget を 1 個ツリーに持つ」形で仮想化していない。SKILL.md にも「数百個の widget をツリーに置くと Build/Layout/ハッシュが毎フレーム全部に効くので表示範囲だけ add するべき」とあり、数百発言の会話では `Measure` の呼び出し量が気になる（未計測。改善案は「会話も `List` ベースにする」か「`Panel` + 可視範囲だけ add」のどちらか）。

## 未検証・次に試したいこと

- 実データ（数千セッション・長い会話）での起動時間・スクロールの体感、長い会話での `Measure` コスト
- 文字入力の E2E（vmhost では `TypeRune` が TextInput に届かない。`exp/textinput` の注入か、`GUIGUI_DEBUG=emulateclipboard` + 貼り付けでカバーする）
- システムのクリップボードへの実書き込み（WSLg では clipboard のポーリングが `read timeout` のログを出していた。`emulateclipboard` 経路は動作する）
- WebAssembly での実行（Ebitengine は js/wasm に対応。guigui も理論上は動くはず）
- `ebitenginewidget`（別プロセスの ebiten アプリを子 widget として内包。今回の用途には要らないが面白い）
- `example/codeeditor` / `richtexteditor` / `toast` / `menubar` / `drawer` あたりの部品寄せ（会話ビューでの適用余地）
- 会話の Markdown レンダリング（`Text` の override styles で自前実装するか、行単位のプレーンテキストのままにするか）

## 関連

- 試作: `apps/agent-history/guigui/main.go`
- mygo の概要と書き方: [mygo.md](./mygo.md)
- nsgo での同種の試作（対応表・mygo との比較検証つき）: [nsgo.md](./nsgo.md)
- guigui 公式のエージェント向け手引き: リポジトリ `skills/using-guigui/SKILL.md`
- Ebitengine のヘッドレス駆動: `skills/run-ebitengine-app-headless/SKILL.md`（ebiten モジュール内）
