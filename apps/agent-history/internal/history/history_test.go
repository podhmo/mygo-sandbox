package history

import (
	"errors"
	"testing"
	"time"
)

type stubSource struct {
	name     string
	sessions []*Session
	err      error
}

func (s *stubSource) ID() string                        { return s.name }
func (s *stubSource) Name() string                      { return s.name }
func (s *stubSource) AssistantName() string             { return s.name }
func (s *stubSource) Sessions() ([]*Session, error)     { return s.sessions, s.err }
func (s *stubSource) Entries(*Session) ([]Entry, error) { return nil, nil }
func (s *stubSource) ResumeCommand(*Session) string     { return "" }

// 一部の取得元が失敗しても、他の取得元のセッションは返す。エラーには取得元の名前を付ける。
func TestLoadPartialFailure(t *testing.T) {
	broken := errors.New("file is not a database")
	ok := &stubSource{name: "Good", sessions: []*Session{{ID: "a", Cwd: "/tmp/demo", End: time.Now()}}}
	ps, err := Load([]Source{ok, &stubSource{name: "Devin", err: broken}, &stubSource{name: "Empty"}})
	if len(ps) != 1 || len(ps[0].Sessions) != 1 || ps[0].Sessions[0].ID != "a" {
		t.Fatalf("projects=%+v", ps)
	}
	if !errors.Is(err, broken) || err.Error() != "Devin: file is not a database" {
		t.Fatalf("err=%v", err)
	}
}
