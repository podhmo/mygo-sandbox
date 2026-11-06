//go:build e2e

// 実ウィンドウで自作の部品を動かす E2E テスト。client と start は
// examples/counter/e2e_test.go と同じ（automation プラグインのソケット越し）。
//
//	go test -tags e2e ./examples/customwidget
//	SNAPSHOT=/tmp/customwidget-e2e.png go test -tags e2e ./examples/customwidget
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	bin := filepath.Join(dir, "customwidget")
	build := exec.Command("go", "build", "-tags", "e2e", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatal(err)
	}
	// Unix ソケットのパスは 104 バイトまでなので、長い TempDir は避ける。
	sock := filepath.Join(os.TempDir(), "mygo-customwidget-e2e.sock")
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

// anode は tree の要素のうち、このテストで見るもの。
type anode struct {
	Role, Key, Label, Value string
	Now                     *float64
	Actions                 []string
	Bounds                  struct{ X, Y, Width, Height float64 }
}

func (c *client) find(key string) anode {
	c.t.Helper()
	var tr struct{ Nodes []anode }
	if err := json.Unmarshal(c.call("tree", nil), &tr); err != nil {
		c.t.Fatal(err)
	}
	for _, n := range tr.Nodes {
		if n.Key == key {
			return n
		}
	}
	c.t.Fatalf("no node with key %q", key)
	return anode{}
}

func (c *client) idle() { c.call("idle", map[string]any{"timeout": 5000}) }

func TestCustomWidgetsE2E(t *testing.T) {
	c := start(t)
	c.idle()

	// Knob は Role / Label / Range を付けたので slider として見える。
	k := c.find("Volume")
	t.Logf("knob node: role=%s label=%s value=%s now=%v actions=%v", k.Role, k.Label, k.Value, deref(k.Now), k.Actions)
	if k.Role != "slider" || k.Label != "Volume" || k.Now == nil || *k.Now != 0.3 {
		t.Errorf("knob node: %+v", k)
	}

	// OS の入力経路でドラッグ: 中心で押し、上へ 30 DIP 動かして離す。
	x, y := k.Bounds.X+k.Bounds.Width/2, k.Bounds.Y+k.Bounds.Height/2
	c.call("input", map[string]any{"events": []map[string]any{
		{"type": "pointerMove", "x": x, "y": y},
		{"type": "pointerDown", "x": x, "y": y},
	}})
	c.idle()
	c.call("input", map[string]any{"events": []map[string]any{{"type": "pointerMove", "x": x, "y": y - 30}}})
	c.idle()
	c.call("input", map[string]any{"events": []map[string]any{{"type": "pointerUp", "x": x, "y": y - 30}}})
	c.idle()
	if k := c.find("Volume"); k.Now == nil || math.Abs(*k.Now-0.5) > 1e-6 {
		t.Errorf("after drag: now=%v, want 0.5", deref(k.Now))
	}

	// フォーカス中のキー
	c.call("input", map[string]any{"events": []map[string]any{{"type": "key", "key": "Up"}}})
	c.idle()
	if k := c.find("Volume"); k.Now == nil || math.Abs(*k.Now-0.55) > 1e-6 {
		t.Errorf("after Up: now=%v, want 0.55", deref(k.Now))
	}

	// 支援技術の increment は自作の slider に届くか（届かないなら unsupported などが返る）。
	if contains(k.Actions, "increment") {
		c.call("act", map[string]any{"key": "Volume", "action": "increment"})
		c.idle()
		t.Logf("after increment: now=%v", deref(c.find("Volume").Now))
	}

	// Heatmap の升目は要素でないので座標でクリック。選択は Value で読める。
	h := c.find("activity")
	c.call("input", map[string]any{"events": []map[string]any{
		{"type": "click", "x": h.Bounds.X + 2*17 + 7, "y": h.Bounds.Y + 1*17 + 7},
	}})
	c.idle()
	if h := c.find("activity"); !strings.HasPrefix(h.Value, "week 3, day 2:") {
		t.Errorf("heatmap value=%q", h.Value)
	}

	// Chip は ToggleBase なので press が効く。
	c.call("act", map[string]any{"key": "gpu", "action": "press"})
	c.idle()
	t.Logf("chip: %+v", c.find("gpu"))

	var shot struct{ PNG string }
	json.Unmarshal(c.call("capture", nil), &shot)
	if path := os.Getenv("SNAPSHOT"); path != "" {
		b, _ := base64.StdEncoding.DecodeString(shot.PNG)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c.call("menu", map[string]any{"path": []string{"customwidget", "Quit customwidget"}})
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
