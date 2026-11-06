# WSL2 + WSLg での追試（mygo / nsgo on Linux）

[mygo.md](./mygo.md)・[nsgo.md](./nsgo.md) は macOS での調査だったので、同じ版を WSL2 の Linux 上で試した記録。

- 環境: WSL2 + Ubuntu 26.04.1 LTS (resolute) amd64、WSLg 有効（`WAYLAND_DISPLAY=wayland-0`, `DISPLAY=:0`、Weston + XWayland）
- Go 1.27.3、mygo v0.7.4、nsgo `88b0f10`（前回と同じ）
- 日付: 2026-11-06
- **WSL の設定で Windows 側との連携を切っている**: `/etc/wsl.conf` で `[interop] enabled=false`・`appendWindowsPath=false`、`[automount] enabled=false`（`/mnt/c` も空）。これにより「Windows 側のプログラムを呼ぶ系」の機能はことごとく失敗する（後述）。

## 結果のまとめ

### 動いたもの

| 項目 | 結果 |
|---|---|
| `go build ./...` | OK |
| `go test ./...`（全テスト） | OK。mygo の `ui.NewTester` も nsgo の `nsgotest` ヘッドレスもウィンドウ・GTK4 不要でそのまま通る |
| `go tool mygo vet`（全アプリ） | OK |
| `go tool mygo doctor` | go ✓ / gtk(libgtk-3.so.0) ✓ / webkit ✗ / bun・makensis・osslsign は optional で無し |
| mygo Native UI の実ウィンドウ | counter / todo / async / customwidget / agent-history すべて起動。**Wayland 経由でも `WAYLAND_DISPLAY=` を抜いた X11(XWayland) 経由でも動く**。agent-history は実データで `loaded 7 projects in 460ms` |
| automation E2E（`go test -tags e2e`） | 実ウィンドウ起動・automation ソケット・`act`・座標指定の `input`（OS の入力経路）・PNG `capture` まで全部動く。**最後の `menu` 呼び出しだけ失敗**（下記） |
| `mygo dev` | live reload 動作（ビルド→起動→watch） |
| `mygo dev -web` | wasm 11.5 MB をビルドして `http://localhost:5174/` で配信（curl で HTTP 200 確認。表示にはブラウザ + WebGL2 が要る） |
| `mygo build` | linux/amd64: バイナリ 9.1 MB + `counter_0.1.0_amd64.deb` 3.9 MB + tar.gz + PKGBUILD + .desktop + install.sh。生成バイナリも起動 |
| nsgo 実ウィンドウ | `go run ./apps/agent-history/nsgo` が GTK4（ローカル展開、下記）で起動。GLib の `g_value_get_int` 警告は出るが実害なし |
| `nsgo test -target linux` | **9/9 pass**。実ウィンドウ＋ネイティブ入力でテスト、`.nsgo/test/screenshots/linux/` に PNG 保存。日本語・Markdown・バッジなど描画も正しい |
| `nsgo dev` / `nsgo build -target linux_x64` | OK（dev はビルド→起動→watch、build は 16.8 MB のバイナリ、起動確認済み） |
| GPU ハードウェア描画 | `GALLIUM_DRIVER=d3d12` で D3D12 経由の GPU 描画になる。既定のアダプタは **AMD Radeon 780M（内蔵GPU）**、`MESA_D3D12_DEFAULT_ADAPTER_NAME=NVIDIA` で **RTX 5060 Laptop** も選べる（下記） |

### 動かないもの

| 項目 | 結果 |
|---|---|
| `nsgo check` | Go だけのアプリでは失敗（TSX 用に依存を `go list` するので `go.mod updates needed` エラー）。macOS と同じ挙動 |
| mygo automation の `menu` メソッド | `mygo: window has no menu bar` で失敗。Linux のウィンドウにはメニューバーが無い（macOS のアプリメニュー相当が無い）。`examples/counter/e2e_test.go` の最後（Quit だけ）がこれで落ちるが、操作・キャプチャ自体は全て通っている |
| URL を開く系 | `xdg-open` が「ブラウザが見つからない」で失敗。**interop を切っているので Windows 側のブラウザへのフォールバックも無い**。`mygo` の OpenURL 系、nsgo 側も同様に失敗するはず |
| 通知 / トレイ / secrets | セッションバスに `org.freedesktop.Notifications`・`StatusNotifierWatcher`・Secret Service がいずれも存在しない（`org.freedesktop.DBus`, `org.a11y.Bus`, `org.freedesktop.systemd1` のみ）。これらの API は呼んでも先がいない |
| WebView | mygo は `libwebkit2gtk-4.1-0`、nsgo は `libwebkitgtk-6.0-4` が未インストールのため未検証（doctor も webkit ✗） |
| GPU（デフォルト） | `/dev/dri/{card0,renderD128}` が `root:video`/`root:render` 0660 でユーザー `po` が非所属 → `Permission denied` → **llvmpipe による CPU 描画にフォールバック**（`mygo: native UI draws without the GPU` のログが出る） |

## 必要だったもの・あれば嬉しいもの（apt）

sudo が使えなかったので今回はローカル展開で凌いだ（下記）。素直に入れるなら:

```sh
sudo apt install libgtk-4-1            # nsgo の実行に必須（4.22.4。要求は 4.14+）
sudo apt install libwebkit2gtk-4.1-0   # mygo の WebView / web page
sudo apt install libwebkitgtk-6.0-4    # nsgo の WebView
sudo apt install mesa-utils-bin        # glxinfo/eglinfo（GPU 診断）。26.04 では mesa-utils は symlink だけで本体は -bin
sudo apt install libnotify-bin         # notify-send（通知を試すなら。デーモンも別途必要）
sudo apt install wl-clipboard          # Wayland クリップボードの CLI 確認用
sudo usermod -aG video,render po       # /dev/dri を開けるようにする（要再ログイン）
```

- bun: mygo の web フロント雛形を使うなら（optional、今回不要）
- ブラウザ（chromium など）: `nsgo test -target web` や `mygo dev -web` の表示確認を WSL 内でやるなら。Windows 側ブラウザは interop を切っているので呼べない（localhost 転送で手動で開く分には使える可能性あり）

## sudo 無しで GTK4 を入れる（今回使った方法）

purego の `dlopen` は `LD_LIBRARY_PATH` を見るので、deb をローカルに展開すれば動く:

```sh
mkdir -p /tmp/gtk4local/debs /tmp/gtk4local/root
# 依存ごと全部ダウンロード（33 個、17 MB）
apt-get -y --download-only -o Dir::Cache::archives=/tmp/gtk4local/debs -o Debug::NoLocking=1 install libgtk-4-1
cd /tmp/gtk4local/root && for d in /tmp/gtk4local/debs/*.deb; do dpkg -x "$d" .; done
glib-compile-schemas /tmp/gtk4local/root/usr/share/glib-2.0/schemas/

export LD_LIBRARY_PATH=/tmp/gtk4local/root/usr/lib/x86_64-linux-gnu
export XDG_DATA_DIRS=/tmp/gtk4local/root/usr/share:$XDG_DATA_DIRS

go run ./apps/agent-history/nsgo                    # 起動する
go tool nsgo test -target linux ./apps/agent-history/nsgo   # 実ウィンドウテストも通る
```

`ldd libgtk-4.so.1 | grep "not found"` が空になれば OK。GSettings schema（`org.gtk.gtk4.*`）も libgtk-4-common から展開してコンパイルしておくと警告が減る。同じ手順で `mesa-utils-bin`（glxinfo/eglinfo）も入れられた。

## GPU まわり（WSLg + Mesa）

症状は 3 段階:

1. 何もしないと `failed to get driver name for fd -1` / `ZINK: failed to choose pdev` → **llvmpipe（CPU）**で描画。`glxinfo -B` で `Accelerated: no`・`Device: llvmpipe`。
2. 原因は `/dev/dri/card0`・`/dev/dri/renderD128` のパーミッション（`root:video`/`root:render` 0660、ユーザー `po` は `po adm cdrom sudo dip plugdev users`）。udevadm では `/dev/dxg` は 0666 で誰でも開ける。
3. `GALLIUM_DRIVER=d3d12` を付けると D3D12 経路が開き、mygo の `draws without the GPU` ログも消える。`Dropped Escape call with ulEscapeCode : 0x03007703` という libd3d12.so 由来の警告が出ることがあるが、キャプチャまで問題なく動いた（実害は見えていない）。

恒久対応は `sudo usermod -aG video,render po`（再ログイン要）か、環境変数をアプリ起動時に付けるか。d3d12 の DRI ドライバ（`/usr/lib/x86_64-linux-gnu/dri/d3d12_dri.so`）と `/usr/lib/wsl/lib` の libd3d12.so（ldcache 入り）は最初から揃っていたので、ドライバ不足ではなかった。

### どの GPU が使われるか（2 枚構成）

このマシンは **AMD Radeon 780M（Ryzen 7 255 内蔵）+ NVIDIA GeForce RTX 5060 Laptop** のハイブリッド構成で、どちらも dxcore 経由で WSL から見えている。Mesa の d3d12 ドライバは列挙順の先頭を既定にするので、何も指定しないと **780M（内蔵）**が選ばれる（`glxinfo -B` が `D3D12 (AMD Radeon 780M Graphics)` を返した）。

NVIDIA 側で描画させたいなら `MESA_D3D12_DEFAULT_ADAPTER_NAME`（アダプタ名の部分一致）で指定できる:

```console
$ GALLIUM_DRIVER=d3d12 MESA_D3D12_DEFAULT_ADAPTER_NAME=NVIDIA glxinfo -B
OpenGL renderer string: D3D12 (NVIDIA GeForce RTX 5060 Laptop GPU)
```

実際に `MESA_D3D12_DEFAULT_ADAPTER_NAME=NVIDIA` 付きで counter を起動しても動いた。なお `nvidia-smi -L` には NVIDIA しか出ない（CUDA/計算用の別経路で、こちらは最初から 617.42 / CUDA 13.4 で動いている）。Vulkan 側はこの mesa に Dozen（Vulkan-on-D3D12）の ICD が無いため、Vulkan を使うアプリは lavapipe（CPU）に落ちる。

## Windows 側とのアクセスを切っている影響

- **URL を開く系が全部失敗する**（xdg-open → ブラウザ無し）。WSL では通常、Windows 側の既定ブラウザに投げるが interop が無いので無理。`-web` 系は「配信までは動くがブラウザを開けない」形になり、Windows 側から `http://localhost:5174/` を手で開く運用になる（localhost 転送が効いていれば）
- クリップボード共有は WSLg（Weston の RDP バックエンド）が担うので、Wayland 経由のコピペ自体は動く（interop とは別経路）
- 通知・トレイ・secret service はそもそも Linux 側のサービスが無いので、interop とは無関係に失敗
- GUI の表示・入力・キャプチャはすべて WSLg 側で完結するので、今回試した範囲では interop 切断の影響は「URL を開く」だけ

## macOS との差分メモ

- mygo の Native UI は「Go だけで足りる」はそのまま（GTK3 は最初から入っていた）。nsgo は GTK 4.14+ が要るので **Ubuntu 26.04 なら apt 1 個で足りる**
- mygo の automation `menu` は Linux のウィンドウにはメニューバーが無いので、アプリの終了導線を `menu` に依存した e2e テストは Linux では書き換えが要る（ウィンドウを閉じる API かプロセス kill）
- nsgo `nsgo test -target linux` は macOS 版と同じくそのまま動いた。`nsgo check` の失敗も macOS と同じ
- ビルド成果物: mygo は `.deb`/`PKGBUILD`/`install.sh` が出る。nsgo は `-target linux_x64` で素のバイナリ
