package secrets

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var write = flag.Bool("write", false, "rewrite contracts/secrets.jsonl")

// casesPath is the contract's cases, which the Lisp port reads too.
var casesPath = filepath.Join("..", "..", "contracts", "secrets.jsonl")

// Case is one contract case: the input, written as parts joined with nothing between them (so no
// file of the repository holds a whole token a push scanner would stop), and the redacted text.
type Case struct {
	Name   string   `json:"name"`
	Input  []string `json:"input"`
	Output string   `json:"output"`
}

func (c Case) Text() string { return strings.Join(c.Input, "") }

// inputs are the cases' names and inputs; their outputs are this package's, checked by hand when
// they change.
var inputs = []Case{
	{Name: "access key id", Input: []string{"export AWS_ACCESS_KEY_ID=", "AKIA", "Q7ZK3M2XR5TB9WNP", " && aws s3 ls"}},
	{Name: "access key id needs its whole shape", Input: []string{"id ", "AKIA", "Q7ZK3M2XR5", " is short"}},
	{Name: "secret access key by name", Input: []string{"AWS_SECRET_ACCESS_KEY=", "u4Rk9xPq2Lm7Vt3Nw8Ys", "Hb5Jc1Fd6Ge0Za2Qx9Kp"}},
	{Name: "gh token", Input: []string{"git clone https://x:", "gh", "p_", "Zq8Vt2Lm9Xr4Kp7Nw3Ys6Hb1Jc5Fd0Ge8QaT2", "@host/repo.git"}},
	{Name: "gh token in an env file", Input: []string{"GITHUB_TOKEN=", "gh", "s_", "Zq8Vt2Lm9Xr4Kp7Nw3Ys6Hb1Jc5Fd0Ge8QaT2"}},
	{Name: "fine-grained gh token", Input: []string{"use ", "github", "_pat_", "11ABCDEFG0aBcDeFgHiJkL_mNoPqRsTuVwXyZ0123456789", " today"}},
	{Name: "gl token", Input: []string{"PRIVATE-TOKEN: ", "gl", "pat-", "Xr4Kp7Nw3Ys6Hb1Jc5Fd"}},
	{Name: "xox token", Input: []string{"SLACK_BOT=", "xo", "xb-", "2618-90457-Kp7Nw3Ys6Hb1"}},
	{Name: "chat webhook", Input: []string{"POST ", "https://hooks.slack.com/services/", "T0000/B0000/Xr4Kp7Nw3Ys6Hb1Jc5Fd", " done"}},
	{Name: "payment key", Input: []string{"stripe.api_key = '", "sk", "_live_", "51Hb1Jc5Fd0Ge8QaT2Zq8Vt", "'"}},
	{Name: "webhook signing secret", Input: []string{"whsec", "_", "Hb1Jc5Fd0Ge8QaT2Zq8Vt2Lm"}},
	{Name: "sk- key", Input: []string{"key=", "sk", "-ant-api03-", "Hb1Jc5Fd0Ge8QaT2Zq8Vt2Lm9Xr4", " in .env"}},
	{Name: "sk- project key", Input: []string{"OPENAI_API_KEY: ", "sk", "-proj-", "Hb1Jc5Fd0Ge8QaT2Zq8Vt2Lm9Xr4"}},
	{Name: "sk- needs twenty characters", Input: []string{"sk-learn is installed"}},
	{Name: "aiza key", Input: []string{"maps key ", "AI", "za", "SyHb1Jc5Fd0Ge8QaT2Zq8Vt2Lm9Xr4Kp7Nw3Y"}},
	{Name: "jwt", Input: []string{"cookie: session=", "eyJ", "hbGciOiJIUzI1NiJ9.", "eyJ", "zdWIiOiIxMjM0In0.", "Hb1Jc5Fd0Ge8QaT2Zq8Vt2"}},
	{Name: "url credentials", Input: []string{"DATABASE_URL=postgres://admin:", "S3cr3tPw0rd", "@db.internal:5432/app"}},
	{Name: "url credentials with an at sign", Input: []string{"mysql://root:", "p@ss", "w0rd@127.0.0.1/db"}},
	{Name: "url without credentials", Input: []string{"open https://example.com:8443/path?q=1 and http://localhost:3000/a@b"}},
	{Name: "url credentials env reference", Input: []string{"redis://default:${REDIS_PASSWORD}@cache:6379"}},
	{Name: "bearer header", Input: []string{`curl -H "Authorization: Bearer `, "Hb1Jc5Fd0Ge8QaT2Zq8Vt2Lm9Xr4Kp7N", `" https://api.internal/v1`}},
	{Name: "bearer env reference", Input: []string{`curl -H "Authorization: Bearer $TOKEN" https://api.internal/v1`}},
	{Name: "basic in prose", Input: []string{"basic authentication works"}},
	{Name: "password assignment", Input: []string{"mysql -u root --password=", "hunter22x", " -e 'select 1'"}},
	{Name: "password in json", Input: []string{`{"user": "ana", "password": "`, "correct horse battery", `"}`}},
	{Name: "password in escaped json", Input: []string{`{"command":"export DB_PASSWORD=\"`, "Tr0ub4dor&3", `\""}`}},
	{Name: "password in yaml", Input: []string{"  password: ", "Wq9!zL2#vB", "\n  user: ana"}},
	{Name: "password env reference", Input: []string{"PGPASSWORD=$DB_PASS psql -h db"}},
	{Name: "password template reference", Input: []string{`password: "${PASSWORD}"`}},
	{Name: "password placeholder", Input: []string{"password=changeme123 and secret=xxxxxxxxxx and passwd=<your-password>"}},
	{Name: "password too short", Input: []string{"password=abc12"}},
	{Name: "secret name keeps its case", Input: []string{"Client_Secret = ", "Hb1Jc5Fd0Ge8", ";"}},
	{Name: "token assignment", Input: []string{"api_key=", "Hb1Jc5Fd0Ge8QaT2Zq8Vt2"}},
	{Name: "token assignment at the entropy threshold", Input: []string{"API_KEY=aabbccddefghijkl"}},
	{Name: "token assignment below the entropy threshold", Input: []string{"API_KEY=aaabbccdefghijkl"}},
	{Name: "token counts are not secrets", Input: []string{"max_tokens=4096, tokenCount: 563, \"tokenizer\": \"cl100k_base\""}},
	{Name: "token low entropy", Input: []string{"token: abcdabcdabcd"}},
	{Name: "private key block", Input: []string{"-----BEGIN RSA ", "PRIVATE KEY-----\nMIIEpAIBAAKCAQEA7Hb1Jc5Fd0Ge8QaT2Zq8\nVt2Lm9Xr4Kp7Nw3Ys6Hb1Jc5Fd0Ge8QaT2\n-----END RSA ", "PRIVATE KEY-----\nafter"}},
	{Name: "private key on one line, cut short", Input: []string{"cat id: -----BEGIN OPENSSH ", "PRIVATE KEY----- b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAA Hb1Jc5Fd0Ge8QaT2Zq8Vt2 … [truncated 4000 chars]"}},
	{Name: "private key in escaped json", Input: []string{`{"content":"-----BEGIN `, `PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEF\nAASCBKcwggSjAgEAAoIBAQ\n-----END `, `PRIVATE KEY-----\n"}`}},
	{Name: "public key is kept", Input: []string{"-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhkiG9w0BAQEF\n-----END PUBLIC KEY-----"}},
	{Name: "two secrets on one line", Input: []string{"AWS_ACCESS_KEY_ID=", "AKIA", "Q7ZK3M2XR5TB9WNP", " AWS_SECRET_ACCESS_KEY=", "u4Rk9xPq2Lm7Vt3Nw8YsHb5Jc1Fd6Ge0Za2Qx9Kp"}},
	{Name: "already redacted", Input: []string{"password=[redacted:password-assignment] key [redacted:sk-key]"}},
	{Name: "non-ascii value", Input: []string{"token=", "ÄöÜßéñ漢字Hb1Jc5Fd0Ge8"}},
	{Name: "plain prose", Input: []string{"The user asked to rotate the secret keys weekly and to store them in the vault."}},
	{Name: "empty", Input: []string{""}},
}

func outputs() []Case {
	out := make([]Case, len(inputs))
	for i, c := range inputs {
		c.Output = Redact(c.Text())
		out[i] = c
	}
	return out
}

func encodeCases(cases []Case) string {
	var b strings.Builder
	for _, c := range cases {
		line, err := json.Marshal(c)
		if err != nil {
			panic(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// The contract's cases are this package's output, byte for byte (go test ./secrets -args -write
// rewrites them).
func TestContractCases(t *testing.T) {
	got := encodeCases(outputs())
	if *write {
		if err := os.WriteFile(casesPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(casesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("contracts/secrets.jsonl is stale; rerun with -args -write and review the diff\n%s", got)
	}
}

func TestRedactedValuesAreGone(t *testing.T) {
	cases := map[string]string{
		"access key id":                             "Q7ZK3M2XR5TB9WNP",
		"secret access key by name":                 "u4Rk9xPq2Lm7Vt3Nw8Ys",
		"gh token":                                  "Zq8Vt2Lm9Xr4Kp7Nw3Ys",
		"gh token in an env file":                   "Zq8Vt2Lm9Xr4Kp7Nw3Ys",
		"fine-grained gh token":                     "aBcDeFgHiJkL",
		"gl token":                                  "Xr4Kp7Nw3Ys6Hb1Jc5Fd",
		"xox token":                                 "Kp7Nw3Ys6Hb1",
		"chat webhook":                              "Xr4Kp7Nw3Ys6Hb1Jc5Fd",
		"payment key":                               "51Hb1Jc5Fd0Ge8QaT2Zq8Vt",
		"webhook signing secret":                    "Hb1Jc5Fd0Ge8QaT2Zq8Vt2Lm",
		"sk- key":                                   "Hb1Jc5Fd0Ge8QaT2Zq8Vt2Lm9Xr4",
		"sk- project key":                           "Hb1Jc5Fd0Ge8QaT2Zq8Vt2Lm9Xr4",
		"aiza key":                                  "SyHb1Jc5Fd0Ge8QaT2Zq8",
		"jwt":                                       "Hb1Jc5Fd0Ge8QaT2Zq8Vt2",
		"url credentials":                           "S3cr3tPw0rd",
		"url credentials with an at sign":           "p@ssw0rd",
		"bearer header":                             "Hb1Jc5Fd0Ge8QaT2Zq8Vt2Lm9Xr4Kp7N",
		"password assignment":                       "hunter22x",
		"password in json":                          "correct horse battery",
		"password in escaped json":                  "Tr0ub4dor",
		"password in yaml":                          "Wq9!zL2#vB",
		"secret name keeps its case":                "Hb1Jc5Fd0Ge8",
		"token assignment":                          "Hb1Jc5Fd0Ge8QaT2Zq8Vt2",
		"private key block":                         "MIIEpAIBAAKCAQEA",
		"private key on one line, cut short":        "b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAA",
		"private key in escaped json":               "MIIEvQIBADANBgkqhkiG9w0BAQEF",
		"two secrets on one line":                   "u4Rk9xPq2Lm7Vt3Nw8Ys",
		"non-ascii value":                           "Hb1Jc5Fd0Ge8",
		"token assignment at the entropy threshold": "aabbccddefghijkl",
	}
	kept := map[string]string{
		"secret access key by name":  "AWS_SECRET_ACCESS_KEY=",
		"password assignment":        "--password=",
		"secret name keeps its case": "Client_Secret = ",
		"url credentials":            "postgres://admin:",
		"private key block":          "\nafter",
	}
	for _, c := range outputs() {
		if value, ok := cases[c.Name]; ok && strings.Contains(c.Output, value) {
			t.Errorf("%s: %q still holds %q", c.Name, c.Output, value)
		}
		if name, ok := kept[c.Name]; ok && !strings.Contains(c.Output, name) {
			t.Errorf("%s: %q lost %q", c.Name, c.Output, name)
		}
		if again := Redact(c.Output); again != c.Output {
			t.Errorf("%s: not idempotent: %q then %q", c.Name, c.Output, again)
		}
	}
	unchanged := []string{"access key id needs its whole shape", "sk- needs twenty characters", "url without credentials", "url credentials env reference", "bearer env reference", "basic in prose", "password env reference", "password template reference", "password placeholder", "password too short", "token assignment below the entropy threshold", "token counts are not secrets", "token low entropy", "public key is kept", "already redacted", "plain prose", "empty"}
	for _, c := range outputs() {
		for _, name := range unchanged {
			if c.Name == name && c.Output != c.Text() {
				t.Errorf("%s: changed to %q", c.Name, c.Output)
			}
		}
	}
}

func TestEntropyExact(t *testing.T) {
	// 4 characters twice and 8 once in 16: exactly 3.5 bits per character.
	if !EntropyAtLeast("aabbccddefghijkl", 7, 2) {
		t.Error("3.5 bits is at least 3.5")
	}
	if EntropyAtLeast("aabbccddefghijkl", 36, 10) {
		t.Error("3.5 bits is below 3.6")
	}
	if !EntropyAtLeast("abcdefghijklmnop", 4, 1) || EntropyAtLeast("abcdefghijklmnop", 41, 10) {
		t.Error("16 distinct characters are exactly 4 bits")
	}
	if EntropyAtLeast("aaaa", 1, 10) || !EntropyAtLeast("aaaa", 0, 1) {
		t.Error("one repeated character has no entropy")
	}
}

func TestRedactJSON(t *testing.T) {
	value := "u4Rk9xPq2Lm7Vt3Nw8YsHb5Jc1Fd6Ge0Za2Qx9Kp"
	data := map[string]any{"observations": []any{map[string]any{"id": "abc", "content": "AWS_SECRET_ACCESS_KEY=" + value + " <ok>", "password": "kept name"}}}
	got := RedactData(data)
	raw, ok := got.(json.RawMessage)
	if !ok {
		t.Fatalf("not redacted: %#v", got)
	}
	if strings.Contains(string(raw), value) || !strings.Contains(string(raw), `"password":"kept name"`) || !strings.Contains(string(raw), `\u003cok\u003e`) {
		t.Fatalf("got %s", raw)
	}
	clean := map[string]any{"id": "abc", "content": "nothing here"}
	if got := RedactData(clean); got == nil {
		t.Fatal("nil")
	} else if _, isRaw := got.(json.RawMessage); isRaw {
		t.Fatal("clean data was rewritten")
	}
	text := `{"a":"x","b":["password=hunter22x"],"c":{"password":"hunter22x"}}`
	want := `{"a":"x","b":["password=[redacted:password-assignment]"],"c":{"password":"hunter22x"}}`
	if got := RedactJSON(text); got != want {
		t.Fatalf("got %s", got)
	}
}
