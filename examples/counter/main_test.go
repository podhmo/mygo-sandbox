package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestCounter(t *testing.T) {
	s := &counter{}
	tt := ui.NewTester(s.view, 320, 220)
	for _, label := range []string{"+1", "+1", "-1"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if s.n != 1 || !tt.HasText("1") {
		t.Errorf("n=%d texts=%q", s.n, tt.Texts())
	}
}
