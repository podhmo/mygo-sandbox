//go:build e2e

// 実ウィンドウで counter を動かす E2E テスト。ui.NewTester ではなく、
// ビルドしたアプリを別プロセスで起動し、automation プラグインの
// Unix ソケット（改行区切りの JSON）越しに操作する。
//
//	go test -tags e2e ./examples/counter
//	SNAPSHOT=/tmp/counter.png go test -tags e2e ./examples/counter
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type client struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	id   int
}

func (c *client) call(method string, params any) json.RawMessage {
	c.t.Helper()
	c.id++
	line, _ := json.Marshal(map[string]any{"id": c.id, "method": method, "params": params})
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
	answer, err := c.r.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
	var res struct {
		Result json.RawMessage
		Error  *struct{ Code, Message string }
	}
	if err := json.Unmarshal(answer, &res); err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
	if res.Error != nil {
		c.t.Fatalf("%s: %s: %s", method, res.Error.Code, res.Error.Message)
	}
	return res.Result
}

type node struct {
	Role  string
	Key   string
	Label string
}

func (c *client) tree() []node {
	c.t.Helper()
	var tr struct{ Nodes []node }
	if err := json.Unmarshal(c.call("tree", nil), &tr); err != nil {
		c.t.Fatal(err)
	}
	return tr.Nodes
}

// start は e2e タグ付きでアプリをビルドして起動し、ソケットにつなぐ。
func start(t *testing.T) *client {
	t.Helper()
	dir := t.TempDir()
	// メニューのアプリ名は実行ファイル名（mygo.json が無いとき）になる。
	bin := filepath.Join(dir, "counter")
	build := exec.Command("go", "build", "-tags", "e2e", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatal(err)
	}
	// Unix ソケットのパスは 104 バイトまでなので、長い TempDir は避ける。
	sock := filepath.Join(os.TempDir(), "mygo-counter-e2e.sock")
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "MYGO_AUTOMATION="+sock)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	})
	var conn net.Conn
	var err error
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if conn, err = net.Dial("unix", sock); err == nil {
			break
		}
	}
	if err != nil {
		cmd.Process.Kill()
		t.Fatalf("connect %s: %v", sock, err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &client{t: t, conn: conn, r: bufio.NewReader(conn)}
	// ソケットはウィンドウより先に開くので、ウィンドウが出るまで待つ
	// （その前の idle などは no_window になる）。
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		var ws struct{ Windows []struct{ Visible bool } }
		json.Unmarshal(c.call("windows", nil), &ws)
		if len(ws.Windows) > 0 && ws.Windows[0].Visible {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no window")
		}
	}
	return c
}

func TestCounterE2E(t *testing.T) {
	c := start(t)
	c.call("idle", map[string]any{"timeout": 5000})

	// Key を付けた要素は tree の key で見つかり、act で押せる。
	// immediate mode なので、次のフレームの前に同じボタンを 2 回押すと
	// Clicked() は 1 回しか見えない。act ごとに idle で描き終わるのを待つ。
	for _, key := range []string{"inc", "inc", "dec", "inc"} {
		c.call("act", map[string]any{"key": key, "action": "press"})
		c.call("idle", map[string]any{"timeout": 5000})
	}
	nodes := c.tree()
	if len(nodes) == 0 || nodes[0].Role != "text" || nodes[0].Label != "2" {
		t.Errorf("after pressing: %+v", nodes)
	}

	// 座標でのクリック（OS の入力経路を通る）。bounds は DIP、ウィンドウの内容基準。
	var tr struct {
		Nodes []struct {
			Key    string
			Bounds struct{ X, Y, Width, Height float64 }
		}
	}
	json.Unmarshal(c.call("tree", nil), &tr)
	for _, n := range tr.Nodes {
		if n.Key == "dec" {
			b := n.Bounds
			c.call("input", map[string]any{"events": []map[string]any{
				{"type": "click", "x": b.X + b.Width/2, "y": b.Y + b.Height/2},
			}})
		}
	}
	c.call("idle", map[string]any{"timeout": 5000})
	if nodes := c.tree(); nodes[0].Label != "1" {
		t.Errorf("after clicking -1: %+v", nodes)
	}

	// 見た目は capture で PNG（デバイスピクセル）。画面収録の権限は要らない。
	var shot struct {
		PNG           string
		Width, Height int
	}
	json.Unmarshal(c.call("capture", nil), &shot)
	if shot.Width == 0 || shot.Height == 0 {
		t.Errorf("capture: %dx%d", shot.Width, shot.Height)
	}
	if path := os.Getenv("SNAPSHOT"); path != "" {
		b, _ := base64.StdEncoding.DecodeString(shot.PNG)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// アプリのメニューから終了する（macOS ではアプリ名のメニュー）。
	c.call("menu", map[string]any{"path": []string{"counter", "Quit counter"}})
}
