package redact

import (
	"strings"
	"testing"

	"github.com/podhmo/mygo-sandbox/apps/agent-history/internal/history"
)

// ダミーの秘密は形式だけを合わせ、連結で組み立てる (実在のキーに見える文字列をリポジトリに置かない)。
func rep(s string, n int) string { return strings.Repeat(s, n) }

func newTestRedactor() *Redactor {
	return New(Options{
		Home:  "/Users/alice",
		Users: []string{"alice", "Alice Liddell", "Alice", "Liddell", "podhmo", "ab"},
		Hosts: []string{"alices-mbp"},
	})
}

func TestString(t *testing.T) {
	r := newTestRedactor()
	cases := []struct {
		name, in, want string
	}{
		// A. 認証情報
		{"private key", "x\n-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\nBBBB\n-----END OPENSSH PRIVATE KEY-----\ny", "x\n‹redacted:private-key›\ny"},
		{"private key without end", "-----BEGIN RSA PRIVATE KEY-----\nAAAA", "‹redacted:private-key›"},
		{"anthropic", "key: sk-ant-api03-" + rep("x", 30), "key: ‹redacted:anthropic-key›"},
		{"openai", "OPENAI=sk-proj-" + rep("A1", 15), "OPENAI=‹redacted:openai-key›"},
		{"github classic", "ghp_" + rep("a", 36), "‹redacted:github-token›"},
		{"github fine-grained", "github_pat_" + rep("b", 30), "‹redacted:github-token›"},
		{"slack", "xoxb-" + rep("1", 12) + "-abc", "‹redacted:slack-token›"},
		{"aws", "AKIA" + rep("Z", 16), "‹redacted:aws-key›"},
		{"google", "AIza" + rep("c", 35), "‹redacted:google-key›"},
		{"jwt", "eyJ" + rep("a", 10) + "." + rep("b", 10) + "." + rep("c", 10), "‹redacted:jwt›"},
		{"bearer", "Authorization: Bearer " + rep("t", 20), "Authorization: Bearer ‹redacted:token›"},
		{"url credentials", "https://bob:hunter2pass@example.org/x", "https://‹redacted:credentials›@example.org/x"},
		{"url credentials template", "https://x-access-token:${token}@github.com/o/r.git", "https://x-access-token:${token}@github.com/o/r.git"},
		{"kv env", "export GITHUB_TOKEN=abcd1234efgh", "export GITHUB_TOKEN=‹redacted:value›"},
		{"kv json", `{"api_key": "abcd1234efgh"}`, `{"api_key": "‹redacted:value›"}`},
		{"kv camel", "clientSecret=Zx9Zx9Zx9Zx9", "clientSecret=‹redacted:value›"},
		{"kv password", "password: correct-horse", "password: ‹redacted:value›"},
		// A. 除外
		{"kv token count", "max_output_tokens: 128000", "max_output_tokens: 128000"},
		{"kv number", "maxToken=40960000", "maxToken=40960000"},
		{"kv var", "password=$LOGIN_PASSWORD", "password=$LOGIN_PASSWORD"},
		{"kv template", "token=${token}", "token=${token}"},
		{"kv env ref", "apiKey: process.env.API_KEY", "apiKey: process.env.API_KEY"},
		{"kv actions", "token: secrets.GITHUB_TOKEN", "token: secrets.GITHUB_TOKEN"},
		{"kv short", "secret=abc", "secret=abc"},
		{"kv bool", "use_token: false", "use_token: false"},
		{"git sha is not a secret", "commit " + rep("a1", 20), "commit " + rep("a1", 20)},
		// B. 個人情報
		{"email", "mail me: bob.smith+x@corp.co.jp", "mail me: ‹email›"},
		{"github noreply has username", "123+bob@users.noreply.github.com", "‹email›"},
		{"email allow git", "git@github.com:podhmo/x.git", "git@github.com:‹user›/x.git"},
		{"email allow example", "user@example.com a@b.test", "user@example.com a@b.test"},
		{"email allow coauthor", "Co-Authored-By: Claude <noreply@anthropic.com>", "Co-Authored-By: Claude <noreply@anthropic.com>"},
		{"ip", "ssh 192.168.1.20 and 8.8.8.8", "ssh ‹ip› and ‹ip›"},
		{"ip loopback", "http://127.0.0.1:8080 0.0.0.0 255.255.255.0", "http://127.0.0.1:8080 0.0.0.0 255.255.255.0"},
		// C. 環境
		{"home", "/Users/alice/ghq/x and /Users/alice", "~/ghq/x and ~"},
		{"home prefix of other dir", "/Users/alice2/x", "/Users/‹user›/x"},
		{"other home", "/home/carol/.ssh C:\\Users\\dave\\x", "/home/‹user›/.ssh C:\\Users\\‹user›\\x"},
		{"username word", "drwxr-xr-x  alice  staff", "drwxr-xr-x  ‹user›  staff"},
		{"username inside word is kept", "malice", "malice"},
		{"full name first", "Author: Alice Liddell", "Author: ‹user›"},
		{"case insensitive", "ALICE", "‹user›"},
		{"short words ignored", "ab cd", "ab cd"},
		{"host", "alices-mbp.local", "‹host›.local"},
		{"url-encoded home", "file=%2FUsers%2Falice%2Fx.txt", "file=~%2Fx.txt"},
		{"url-encoded other path", "q=%2Ftmp%2Falice%2Fx", "q=%2Ftmp%2F‹user›%2Fx"},
		{"repeated words", "alice alice", "‹user› ‹user›"},
		{"word next to japanese", "aliceさん", "‹user›さん"},
		// D. 指定した語
		{"extra word", "github.com/podhmo/mygo", "github.com/‹user›/mygo"},
		{"plain text", "hello world", "hello world"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := r.String(c.in); got != c.want {
				t.Errorf("\n in:   %q\n got:  %q\n want: %q", c.in, got, c.want)
			}
		})
	}
}

func TestNilRedactor(t *testing.T) {
	var r *Redactor
	if got := r.String("ghp_" + rep("a", 36) + " /Users/zed"); got != "‹redacted:github-token› /Users/‹user›" {
		t.Errorf("got %q", got)
	}
}

func TestJapaneseName(t *testing.T) {
	r := New(Options{Users: []string{"山田太郎"}})
	if got := r.String("作成者: 山田太郎さん"); got != "作成者: ‹user›さん" {
		t.Errorf("got %q", got)
	}
}

func TestEntries(t *testing.T) {
	r := newTestRedactor()
	in := []history.Entry{{Kind: history.KindTool, Tool: "Bash", Text: "cat /Users/alice/.env", Result: "API_KEY=abcd1234efgh"}}
	out := r.Entries(in)
	if out[0].Text != "cat ~/.env" || out[0].Result != "API_KEY=‹redacted:value›" || out[0].Tool != "Bash" {
		t.Errorf("%+v", out[0])
	}
	if in[0].Text != "cat /Users/alice/.env" {
		t.Error("input must not be modified")
	}
}
