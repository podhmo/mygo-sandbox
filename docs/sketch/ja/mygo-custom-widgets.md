# mygo のカスタム widget 調査メモ

mygo に無い部品を自分で作れるか、作るならどう書くかの調査と、実際に作って動かした記録。

- 環境: macOS 15.8.1 (arm64), Go 1.27.3, mygo v0.7.4
- 日付: 2026-11-06
- 一次資料（モジュール同梱。`go list -m -f '{{.Dir}}' github.com/egoist/mygo` の下）:
  `docs/ui/custom-widgets.md`（入口）, `docs/ui/drawing.md`（Painter）, `docs/ui/input.md`（入力）, `docs/ui/accessibility.md`（Role・状態）, `docs/ui/text-input-client.md`（自前のテキスト編集）, `docs/ui/shaders.md`
- 作った例: [`examples/customwidget/`](../../../examples/customwidget)

## 前提の訂正: mygo は Ebitengine の上に作られていない

「mygo は Ebitengine の上に作られている」と見込んでいたが、そうではない。

- mygo の `go.mod` にあるのは `github.com/ebitengine/purego` だけ。これは Ebitengine の作者が同じ org で出している、**cgo なしで C の関数（OS の API）を呼ぶためのライブラリ**で、ゲームエンジン本体（`hajimehoshi/ebiten`）ではない
- 描画は mygo が自前で持つ: macOS は Metal、Windows は Direct3D 11（駄目なら WARP）、Linux は OpenGL（GtkGLArea）、ブラウザは WebGL 2。GPU が使えないときは CPU で描く（`MYGO_GPU=0` で強制。`docs/ui/rendering.md`）。角丸・枠線・影は距離場のシェーダーで描き、文字はグリフのアトラスから描く
- なので「Ebitengine の `ebiten.Image` に直接描く」という逃げ道は無い。ただ、その代わりに次のような **公式のカスタム widget の仕組み** があり、Ebitengine で描くのと同じ程度に低いところまで降りられる

## mygo の「widget」とは何か

mygo の widget は、**インターフェースを実装して登録する型ではない**。要素（`ui.Element`）を作って返す **ただの関数** である（immediate mode なので、view が毎フレームそれを呼ぶ）。組み込みの `ui.Button` も `ui.Slider` も同じ形をしている。

```go
func Knob(c *ui.Context, v *float64, label string) ui.Element
```

なので「無い widget を作る」とは、次の部品を組み合わせて、この形の関数を書くことになる。

| 層 | 使うもの | 用途 |
|---|---|---|
| 1. 組み合わせる | `ui.Box/Row/Column` + スタイル（`Padding/Radius/Background/...`）+ 既存の部品 | 見た目の組み合わせで済むもの |
| 2. 見た目の無い base | `ButtonBase`, `ToggleBase`, `CheckboxBase`, `SliderBase`, `TabsBase`, `SegmentedBase`, `SelectBase`, `ComboboxBase`, `PopoverBase`, `DialogBase`, `TooltipBase`, `ToastBase`, `TextInputBase` など | 振る舞い（ポインタ・キー・フォーカス・アクセシビリティ）は既存のまま、**見た目だけ** 自分で作る。Web の headless UI（Radix など）に当たる |
| 3. 自分で描く | `Element.Draw` / `DrawOver` と `*ui.Painter` | 要素では表せない絵（円弧、升目、波形など） |
| 4. 自分で入力を受ける | `Hovered/Pressed/Clicked/Dragged/PointerPosition`、`Element.Shortcut`、`HandleInput`（全キー・スクロール・ピンチ） | 独自の操作 |
| 5. 状態を持つ | `ui.Local[T](elem, key, init)` | widget 自身がフレームをまたいで持つ状態（開閉、ホバー中の位置など） |
| 6. 外に伝える | `SetChanged()`（→ 呼び出し側の `Changed()` が true）、`Role/Label/Value/Range/Step/Checked/Expanded`（支援技術） | 組み込みの部品と同じ使い心地にする |
| その先 | `ui.Canvas`（テクスチャへの描画）、シェーダー（Go 構文）、`HandleTextInput`（自前のテキストバッファと IME） | ゲーム的な描画、エディタ |

どの層でも、自作の widget は組み込みの部品と同じ扱いを受ける: Tab でのフォーカス移動、フォーカスリング、テスター（`ui.NewTester`）からの操作、automation プラグインの要素の木、インスペクター。

## 書き方の要点

### 入力は「この組み立ての間に問い合わせる」

```go
box := ui.Box(c).Size(64, 64).Focusable().Cursor(ui.CursorResizeNS)
if _, dy, ok := box.Dragged(); ok { // 前のフレームから押したまま動いた量
	*v -= float64(dy) / 150
}
if box.Shortcut(0, ui.KeyUp) {       // この要素にフォーカスがあるときのキー
	*v += 0.05
}
if x, y, over := box.PointerPosition(); over { ... } // 要素内の座標。聞くとポインタが動くたびにフレームが来る
```

- `Dragged` は要素を押している間に動いた量（要素の座標系）。押したままポインタが要素の外に出た場合は確かめていない
- `Clicked` は「クリックされたか」だけで、**どこをクリックしたかは返さない**。最初は `Clicked()` のフレームの `PointerPosition` で升目を決めていたが、移動・押す・離すが 1 フレームの間にまとめて届くと（automation の `click` など）ポインタがまだ升目の上に来ていないことがあり、実ウィンドウの E2E が時々落ちた。`HandleInput` で `InputPointerDown` の座標（要素基準）を `ui.Local` に覚え、`Clicked()` のときにそれを使うようにして安定した（`false` を返せば Clicked やフォーカスは普段どおり動く）
- 毎キー・IME・スクロール・ピンチまで自分で受けたいときは `HandleInput(func(ui.InputEvent) bool)`。ただし次の組み立てより前に呼ばれるコールバックなので、そこでは `SetChanged` などの組み立て中の操作はできない

### 描くのは `Draw`、描く関数では状態を読まない

```go
value := float32(*v) // 組み立て中に値を写す
box.Draw(func(p *ui.Painter, r ui.Rect) { // r は要素の箱（DIP）
	p.StrokePath(arc(cx, cy, radius, 0, value), 5, t.Accent)
})
```

- `Draw` の関数は **1 フレームに何回か呼ばれることがある**ので、描くだけにする（副作用は書かない）
- Painter でできること: `Fill/Stroke/Shadow`（角丸の四角）、グラデーション、`Path`（`MoveTo/LineTo/QuadTo/CubeTo/Circle`）の `FillPath/StrokePath`、`Text/RichText/MeasureText`、`Image/ImageRegion/Icon`、`Clip`、`Transform`、`FocusRing`
- **円弧の命令は無い**（`Circle` は閉じた円だけ）。円弧は短い線分でつないで作った（Knob の `arc`）
- 動くものは `p.Now()` から計算して `p.AnimationFrame()` を呼ぶと、view を組み立て直さず描き直しだけになる

### 状態は外から渡すか、`ui.Local` に置く

- 値（`*float64` など）は組み込みの部品と同じく呼び出し側からポインタで受ける
- widget だけが知っていればよい状態は `ui.Local(box, "shown", func() bool { return false })` で要素に持たせる（`docs/ui/custom-widgets.md` の Spoiler の例）。**要素や `*ui.Context` を自分で保存してはいけない**（`mygo vet` が見る）
- 状態を持つ widget は、呼ぶ側で `c.Key(...)` を付けて同一性を与える

### 組み込みと同じに見せる

- 値が変わったら `box.SetChanged()`。呼び出し側は `if widget.Knob(...).Changed() { ... }` と書ける
- `Role(ui.RoleSlider)` + `Label` + `Range(lo, hi, v)` + `Step` で、支援技術には slider として渡る（automation の `tree` に `role: slider, now: 0.3` で出るのを確認。VoiceOver で読ませてはいない）
- **支援技術の increment / decrement は、自作の slider にもそのまま届く**。mygo は increment をその要素へのフォーカスと **Right キー**（stepper と右から左のレイアウトでは Up）に置き換えて送るので（`ui/access.go`）、矢印キーを受けていれば別に何か書く必要は無い。実ウィンドウの E2E で 0.55 → 0.6 になるのを確かめた
- 自分の形のフォーカスリングを描くなら `FocusRing(false)` にしてから、`FocusVisible()` のときに `p.FocusRing(rect, radii)`

## 作った例: `examples/customwidget`

`widget` パッケージに mygo に無い部品を 2 つと、base を使った例を 1 つ置き、`main.go` で並べた。

| 部品 | 層 | 内容 |
|---|---|---|
| `widget.Knob` | 3, 4, 6 | 円形のつまみ（mygo の Slider に当たるもの）。上下のドラッグ、矢印・Home・End、slider の Role・Range・Value。円弧と針を Painter で描き、フォーカスリングも円で描く |
| `widget.Heatmap` | 3, 4, 6 | GitHub の contributions のような週×曜日の升目（`ui/chart` にも無い）。升目は要素でなく描くだけなので、`PointerPosition` から hit test してホバーとクリックを判定する。矢印で選択が動く。選んだ升目を `Value` で支援技術に伝える |
| `widget.Chip` | 2 | `ToggleBase` に丸いタグの見た目を付けただけ。クリック・Space・toggle button の Role は base が持つ |

```sh
go run ./examples/customwidget
go test ./examples/customwidget                              # Tester（ウィンドウ無し）
SNAPSHOT=/tmp/cw.png go test -run Snapshot ./examples/customwidget
go test -tags e2e ./examples/customwidget                    # 実ウィンドウ（automation プラグイン）
go tool mygo vet examples/customwidget examples/customwidget/widget
```

### 動作確認の結果

| 項目 | 結果 |
|---|---|
| `go test`（Tester） | OK。Knob: `Find("Volume")`（Label で見つかる）→ `Press/Move/Release` で 30 DIP 上へドラッグして 0.3 → 0.5、`Key(Up)` ×2 で 0.6、End で 1、それより上には行かない。`Changed()` が 4 回。Heatmap: `ClickAt` で升目を選び、隙間のクリックでは変わらない、Right で次の週。Chip: クリックと Space で切り替わる |
| スナップショット（`tt.Image()`） | OK。円弧・針・升目・選択枠・チップが描けている |
| `go test -tags e2e`（実ウィンドウ） | OK。automation の `tree` で Knob が `role=slider label=Volume value=30% now=0.3 actions=[press focus increment decrement]`。OS の入力経路の `pointerDown → pointerMove → pointerUp` で 0.5、`key Up` で 0.55、`act increment` で 0.6。Heatmap は座標の `click` で `value` が `week 3, day 2: …`。Chip は `toggleButton` として `press` が効く。capture（ダークモード）でも見た目が崩れない |
| `go tool mygo vet` | 問題なし |

### 分かったこと・ハマりどころ

- テスターの `Find` / `Click` はテキストか `Label` で要素を探すので、テキストを持たない自作 widget には **`Label` を付けておくとテストしやすい**（自動で支援技術向けの名前にもなる）
- 描くだけの部分（Heatmap の升目）は要素ではないので、テスターでも E2E でも **座標** で操作する。widget の箱は `tt.Find` / `tree` の `bounds` で取れるので、そこからの相対で計算する
- 戻り値を `ui.Element` にしておくと、呼び出し側で `Changed()` を見たり、さらにスタイルを足したりできる。`Heatmap` のようにホバー中の位置など付加情報も返したいときは複数の戻り値にした
- 自作 widget の下に値のテキストを置くと、数字で文字幅が変わり、横に並べた隣の部品が揺れる（左の Knob を回すと右の Knob が動いた）。テキストを含む列に `Width` を付けて固定した。Tester で隣の要素の `Find` の X が動かないことを確かめるテストも置いた
- `Chip` のように既存の振る舞いで足りるなら、Draw から書くより base を使う方が短く、キーボード・フォーカス・アクセシビリティも揃う。Draw から書くのは base に無い操作（回す、升目を選ぶ）のときだけでよい

## まだ試していないもの

- `HandleInput` で全キーやホイールを受ける widget（ターミナルプラグインがこの形）
- `HandleTextInput` と retained text geometry での自前のテキスト編集
- `ui.Canvas` への描画と、シェーダー（`docs/ui/shaders.md`）
- 自前のオーバーレイ（`AttachTo`, `PressedOutside`, `OverlayShortcut`, `Modal`。`docs/ui/overlays.md`）
- Game プラグイン（固定ステップのループ、2D カメラ、ゲームパッド）。Ebitengine 的なことがしたいならこちらが近い
