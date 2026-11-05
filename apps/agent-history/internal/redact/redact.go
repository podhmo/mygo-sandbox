// Package redact は表示する文字列から、認証情報・個人情報・環境を特定できる
// 情報 (ホームディレクトリ、ユーザー名、ホスト名など) を伏せる。
//
// 画面共有やスクリーンショットで履歴を見せるときのためのもので、元のファイルは
// 書き換えない。パターンによる検出なので、すべてを伏せられる保証は無い。
package redact

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
)

// マスク後の表記。Markdown として解釈されない記号 (‹ ›) で囲む。
const (
	markUser  = "‹user›"
	markHost  = "‹host›"
	markEmail = "‹email›"
	markIP    = "‹ip›"
)

func secret(kind string) string { return "‹redacted:" + kind + "›" }

type rule struct {
	hints []string // 小文字にした s がどれも含まなければ正規表現を実行しない (高速化)
	re    *regexp.Regexp
	repl  func(m []string) string
}

// 認証情報。上から順に適用する (具体的なものを先に)。
var secretRules = []rule{
	{[]string{"private key"}, regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
		func([]string) string { return secret("private-key") }},
	{[]string{"sk-ant-"}, regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`), func([]string) string { return secret("anthropic-key") }},
	{[]string{"sk-"}, regexp.MustCompile(`\bsk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_-]{20,}`), func([]string) string { return secret("openai-key") }},
	{[]string{"gh", "github_pat_"}, regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{22,})`), func([]string) string { return secret("github-token") }},
	{[]string{"xox"}, regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), func([]string) string { return secret("slack-token") }},
	{[]string{"akia", "asia"}, regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), func([]string) string { return secret("aws-key") }},
	{[]string{"aiza"}, regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), func([]string) string { return secret("google-key") }},
	{[]string{"eyj"}, regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), func([]string) string { return secret("jwt") }},
	// scheme://user:password@host (パスワードが $VAR / ${var} のテンプレートなら伏せない)
	{[]string{"://"}, regexp.MustCompile(`\b([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s:@'"]+:([^/\s@'"]+)@`),
		func(m []string) string {
			if strings.HasPrefix(m[2], "$") {
				return m[0]
			}
			return m[1] + secret("credentials") + "@"
		}},
	// Authorization: Bearer xxx / Basic xxx
	{[]string{"bearer", "basic"}, regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[A-Za-z0-9._~+/=-]{16,}`),
		func(m []string) string { return m[1] + m[2] + secret("token") }},
	// password=xxx, "api_key": "xxx", SECRET: xxx, export GITHUB_TOKEN=xxx
	{[]string{"passw", "secret", "token", "key", "credential"}, regexp.MustCompile(`(?i)\b([A-Za-z0-9_-]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|credential)s?)(["']?\s*[:=]\s*["']?)([^\s"'<>,;]+)`),
		func(m []string) string {
			if !secretValue(m[1], m[3]) {
				return m[0]
			}
			return m[1] + m[2] + secret("value")
		}},
}

var numberRe = regexp.MustCompile(`^[0-9_.,]+$`)

// secretValue は秘密らしいキーへの代入のうち、値を伏せるべきかを決める。
// 数値 (max_tokens: 4096)、変数・式の参照、短い値、真偽値などは伏せない。
func secretValue(key, value string) bool {
	k, v := strings.ToLower(key), strings.ToLower(value)
	if strings.HasSuffix(k, "tokens") { // トークン数
		return false
	}
	if len(value) < 8 || numberRe.MatchString(value) {
		return false
	}
	for _, p := range []string{"‹", "$", "process.env", "os.environ", "os.getenv", "secrets.", "env."} {
		if strings.HasPrefix(v, p) {
			return false
		}
	}
	switch v {
	case "true", "false", "null", "none", "undefined":
		return false
	}
	return true
}

var (
	emailRe = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`)
	ipv4Re  = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`)
	// 他のユーザーのホームディレクトリ (自分のものは先に ~ にする)
	homesRe = regexp.MustCompile(`(/Users/|/home/|[A-Za-z]:\\Users\\)([^/\\\s'"]+)`)
)

// publicEmail は伏せなくてよいメールアドレス風の文字列か。
// git の SSH URL、例示用のドメイン、コミットの Co-Authored-By など。
func publicEmail(addr string) bool {
	addr = strings.ToLower(addr)
	local, domain, _ := strings.Cut(addr, "@")
	if local == "git" || addr == "noreply@anthropic.com" {
		return true
	}
	switch domain {
	case "example.com", "example.org", "example.net", "example.co.jp":
		return true
	}
	for _, tld := range []string{".example", ".test", ".invalid", ".localhost"} {
		if strings.HasSuffix(domain, tld) {
			return true
		}
	}
	return false
}

// Redactor は伏せる処理。nil やゼロ値でも、認証情報・メール・IP・他人のホームは伏せる。
// 対象の定義は docs/sketch/ja/agent-history-masking.md。
type Redactor struct {
	home       string           // 自分のホームディレクトリ (~ にする)
	words      []*regexp.Regexp // ユーザー名・氏名・ホスト名など (‹user› / ‹host› にする)
	lowerWords []string         // words の元の語 (小文字)。含まれなければ正規表現を実行しない
	marks      []string
}

// Options は環境に依存する伏せ字の対象。
type Options struct {
	Home  string   // ~ にするディレクトリ
	Users []string // ‹user› にする語 (ユーザー名、氏名など)
	Hosts []string // ‹host› にする語
}

// New は Options から Redactor を作る。2 文字以下の語は誤爆が多いので無視する。
func New(opt Options) *Redactor {
	r := &Redactor{home: strings.TrimRight(opt.Home, "/\\")}
	add := func(words []string, mark string) {
		// 長い語を先に (氏名 "Foo Bar" をユーザー名 "foo" より先に)
		ws := append([]string(nil), words...)
		sort.Slice(ws, func(i, j int) bool { return len(ws[i]) > len(ws[j]) })
		for _, w := range ws {
			w = strings.TrimSpace(w)
			if utf8.RuneCountInString(w) <= 2 {
				continue
			}
			pat := regexp.QuoteMeta(w)
			if isASCIIWord(w) {
				// 前後が英数字でないこと。Go の正規表現には後読みが無いので前後の 1 文字も一致させ、
				// 置き換えで戻す。%2F (URL エンコードされた /) の直後も区切りとみなす。
				pat = `(^|[^A-Za-z0-9_]|%2[Ff])(` + pat + `)($|[^A-Za-z0-9_])`
			} else {
				pat = `()(` + pat + `)()`
			}
			r.words = append(r.words, regexp.MustCompile(`(?i)`+pat))
			r.lowerWords = append(r.lowerWords, strings.ToLower(w))
			r.marks = append(r.marks, mark)
		}
	}
	add(opt.Users, markUser)
	add(opt.Hosts, markHost)
	return r
}

// FromEnvironment は実行中のユーザーとマシンから Redactor を作る。extra は追加で
// ‹user› にする語 (GitHub のユーザー名、会社名など)。
func FromEnvironment(extra []string) *Redactor {
	opt := Options{Users: extra}
	if home, err := os.UserHomeDir(); err == nil {
		opt.Home = home
		opt.Users = append(opt.Users, filepath.Base(home))
	}
	if u, err := user.Current(); err == nil {
		opt.Users = append(opt.Users, u.Username, u.Name)
		opt.Users = append(opt.Users, strings.Fields(u.Name)...) // 姓・名だけで出てくる場合
	}
	if h, err := os.Hostname(); err == nil {
		h = strings.TrimSuffix(h, ".local")
		opt.Hosts = append(opt.Hosts, h)
		if short, _, ok := strings.Cut(h, "."); ok {
			opt.Hosts = append(opt.Hosts, short)
		}
	}
	return New(opt)
}

// String は s の伏せるべき箇所を置き換えた文字列を返す。
func (r *Redactor) String(s string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(s) // 手がかりの判定用。置き換えで古くなっても、余分に正規表現を実行するだけ
	for _, rl := range secretRules {
		if containsAny(lower, rl.hints) {
			s = replace(rl.re, s, rl.repl)
		}
	}
	if strings.Contains(s, "@") {
		s = emailRe.ReplaceAllStringFunc(s, func(m string) string {
			if publicEmail(m) {
				return m
			}
			return markEmail
		})
	}
	s = ipv4Re.ReplaceAllStringFunc(s, func(m string) string {
		if m == "127.0.0.1" || m == "0.0.0.0" || strings.HasPrefix(m, "255.") {
			return m
		}
		return markIP
	})
	if r != nil && r.home != "" {
		s = replaceHome(s, r.home)
		if strings.Contains(lower, "%2f") { // URL エンコードされたホーム (%2FUsers%2Falice)
			s = replaceHomeEncoded(s, r.home)
		}
	}
	if containsAny(lower, []string{"/users/", "/home/", "\\users\\"}) {
		s = replace(homesRe, s, func(m []string) string { return m[1] + markUser })
	}
	if r != nil {
		for i, re := range r.words {
			if !strings.Contains(lower, r.lowerWords[i]) {
				continue
			}
			// 連続する語 ("alice alice") は区切りの 1 文字を共有するので、変わらなくなるまで繰り返す
			for range 3 {
				next := re.ReplaceAllString(s, "${1}"+r.marks[i]+"${3}")
				if next == s {
					break
				}
				s = next
			}
		}
	}
	return s
}

// replaceHome は home とその下のパスを ~ にする (/Users/foo2 のような別ディレクトリは除く)。
func replaceHome(s, home string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, home)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(home)
		if end < len(s) && !strings.ContainsRune("/\\ \t\n'\":;,)]}`", rune(s[end])) {
			b.WriteString(s[:end]) // 続きがある別の名前
		} else {
			b.WriteString(s[:i])
			b.WriteString("~")
		}
		s = s[end:]
	}
}

// replaceHomeEncoded は %2F で区切られたホーム (/Users/alice → %2FUsers%2Falice) を ~ にする。
func replaceHomeEncoded(s, home string) string {
	enc := strings.ReplaceAll(filepath.ToSlash(home), "/", "%2F")
	re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(enc) + `(%2F|$|[^A-Za-z0-9_])`)
	return re.ReplaceAllString(s, "~$1")
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func replace(re *regexp.Regexp, s string, fn func([]string) string) string {
	if !re.MatchString(s) {
		return s
	}
	return re.ReplaceAllStringFunc(s, func(m string) string { return fn(re.FindStringSubmatch(m)) })
}

// Entries は各 Entry の文字列を伏せた複製を返す。
func (r *Redactor) Entries(es []history.Entry) []history.Entry {
	out := make([]history.Entry, len(es))
	for i, e := range es {
		e.Text = r.String(e.Text)
		e.Result = r.String(e.Result)
		out[i] = e
	}
	return out
}

func isASCIIWord(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf {
			return false
		}
	}
	first, last := s[0], s[len(s)-1]
	isWord := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	return isWord(first) && isWord(last)
}
