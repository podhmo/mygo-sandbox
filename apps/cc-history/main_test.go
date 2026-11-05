package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// newTestApp は非同期処理を同期にした app を返す。
func newTestApp(t *testing.T) (*app, *[]string) {
	t.Helper()
	a := newApp(filepath.Join("testdata", "projects"))
	var copied []string
	a.copyText = func(s string) { copied = append(copied, s) }
	a.loadEntries = func(s *Session) {
		es, err := LoadEntries(s.File)
		a.setEntries(s, es, err)
	}
	a.setProjects(LoadProjects(a.root))
	return a, &copied
}

func TestViewer(t *testing.T) {
	a, copied := newTestApp(t)
	tt := ui.NewTester(a.view, 1000, 700)

	if !tt.HasText("Choose a session") || !tt.HasText("Go の hello world") || !tt.HasText("readme-fix") {
		t.Fatalf("texts=%q", tt.Texts())
	}

	if err := tt.Click("Go の hello world"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, s := range []string{"Go で hello world を書いて", "完了しました。", "ついでにテストも"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q: texts=%q", s, tt.Texts())
		}
	}
	if tt.HasText("考え中") {
		t.Errorf("thinking is hidden by default")
	}

	// ツールは既定で非表示。表示すると折りたたまれた行が出て、開くと結果が見える
	if err := tt.Click("Tools"); err != nil {
		t.Fatal(err)
	}
	toolTitle := ""
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "⚙ Bash") {
			toolTitle = s
		}
	}
	if toolTitle == "" {
		t.Fatalf("no tool row: %q", tt.Texts())
	}
	if err := tt.Click(toolTitle); err != nil {
		t.Fatal(err)
	}
	tt.Advance(1e9)
	if !tt.HasText("hello world") {
		t.Errorf("tool result not shown: %q", tt.Texts())
	}

	if err := tt.Click("Copy resume command"); err != nil {
		t.Fatal(err)
	}
	if len(*copied) != 1 || (*copied)[0] != `cd "/tmp/demo" && claude --resume 11111111-0000-0000-0000-000000000001` {
		t.Errorf("copied=%q", *copied)
	}

	// 検索で絞り込み
	if err := tt.Click("Search sessions"); err != nil {
		t.Fatal(err)
	}
	tt.Type("readme")
	if len(a.rows) != 2 || a.rows[1].session.Title != "readme-fix" || !tt.HasText("1 session") {
		t.Errorf("rows=%d texts=%q", len(a.rows), tt.Texts())
	}

	if p := os.Getenv("SNAPSHOT"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		png.Encode(f, tt.Image())
	}
}
