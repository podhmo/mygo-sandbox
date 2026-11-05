// Package sqlitedb は、エージェントが使っている SQLite の DB を読み取り専用で開く。
// Devin や OpenCode は実行中も WAL モードで書き込んでいるので、immutable では開かず
// mode=ro で開く (WAL の内容も読める)。ドライバは cgo 不要の modernc.org/sqlite。
package sqlitedb

import (
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// OpenReadOnly は path の DB を読み取り専用で開く。ファイルが無ければ nil, nil を返す。
func OpenReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
