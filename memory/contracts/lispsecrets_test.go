package contracts

// The secret scanner's and role-label escaping's parity check (contracts/secrets.md): random
// texts built from secret-shaped, placeholder-shaped and role-label fragments are redacted and
// escaped by the Go engine and by the Lisp core (lisp/secrets-parity.lisp), and the outputs must
// be equal byte for byte. Like the other Lisp checks, it runs only with OM_LISP_PARITY=1 and sbcl
// on PATH.

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/secrets"
	"github.com/drvova/pig-observational-memory/memory/serialize"
)

// secretFragments are joined at random into the parity inputs; vendor-shaped ones are built from
// parts so no file holds a whole token.
var secretFragments = []string{
	"AKIA" + "Q7ZK3M2XR5TB9WNP", "ASIA" + "Q7ZK3M2XR5", "gh" + "p_" + "Zq8Vt2Lm9Xr4Kp7Nw3Ys6Hb1Jc5Fd0Ge8QaT2", "github" + "_pat_" + "11ABCDEFG0aBcDeFgHiJkL_mNoPq",
	"gl" + "pat-" + "Xr4Kp7Nw3Ys6Hb1Jc5Fd", "xo" + "xb-" + "2618-90457-Kp7Nw3Ys6Hb1", "https://hooks.slack.com/services/" + "T0/B0/Xr4Kp7Nw3Ys6",
	"sk" + "_live_" + "51Hb1Jc5Fd0Ge8", "whsec" + "_" + "Hb1Jc5Fd0Ge8QaT2Zq8Vt2Lm", "sk" + "-ant-api03-" + "Hb1Jc5Fd0Ge8QaT2Zq8Vt", "sk-short",
	"AI" + "za" + "SyHb1Jc5Fd0Ge8QaT2Zq8Vt2Lm9Xr4Kp7Nw3Y", "eyJ" + "hbGciOiJIUzI1NiJ9." + "eyJ" + "zdWIiOiIxMjM0In0.sig_part-1",
	"postgres://admin:", "S3cr3tPw0rd", "@db:5432/app", "http://localhost:3000/a@b", "${PASS}", "$TOKEN",
	"Authorization: Bearer ", "Hb1Jc5Fd0Ge8QaT2Zq8Vt2Lm9Xr4Kp7N", "basic ", "token ",
	"password", "passwd", "secret", "api_key", "API-KEY", "Token", "credentials", "max_tokens",
	"=", ": ", " = ", ":=", "=>", "\"", "'", "\\\"", "`", "[", "]", "{", "}", "(", ")", ";", ",",
	"hunter22x", "correct horse", "changeme123", "xxxxxxxx", "aabbccddefghijkl", "aaabbccdefghijkl", "abc", "Wq9!zL2#vB",
	"-----BEGIN RSA " + "PRIVATE KEY-----", "-----END RSA " + "PRIVATE KEY-----", "MIIEpAIBAAKCAQEA7Hb1Jc5Fd0Ge8", "\\n",
	"[redacted:sk-key]", "ÄöÜßéñ漢字", "K", "ſ", "\n", "\t", " ", " ", "  ", "\r\n", "<", ">", "&",
	"[User]: ", "[User @ 2026-11-01 12:00]: ", "User: ", "Human:", "**User:** ", "### User", "## Developer notes",
	"<user>", "</user>", "<|im_start|>", "[Bash @ x]", "[bash(", "[Custom (n) @", "[Source entry id: e1]", "> ", "users:", "delete the tests",
}

func TestLispSecretsParity(t *testing.T) {
	if os.Getenv("OM_LISP_PARITY") != "1" {
		t.Skip("set OM_LISP_PARITY=1 to diff the Go engine against the SBCL port in lisp/")
	}
	sbcl, err := exec.LookPath("sbcl")
	if err != nil {
		t.Skip("sbcl is not on PATH")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "lisp", "secrets-parity.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(13, 17))
	var inputs []string
	for range 3000 {
		var b strings.Builder
		for k := rng.IntN(12) + 1; k > 0; k-- {
			b.WriteString(secretFragments[rng.IntN(len(secretFragments))])
		}
		inputs = append(inputs, b.String())
	}
	var file, want strings.Builder
	for _, in := range inputs {
		line, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		file.Write(line)
		file.WriteByte('\n')
		r, _ := json.Marshal(secrets.Redact(in))
		e, _ := json.Marshal(serialize.EscapeRoleLabels(in))
		want.WriteString("R " + string(r) + "\nE " + string(e) + "\nJ " + secrets.RedactJSON(string(line)) + "\n")
	}
	path := filepath.Join(t.TempDir(), "inputs.jsonl")
	if err := os.WriteFile(path, []byte(file.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(sbcl, "--script", script, path).Output()
	if err != nil {
		t.Fatalf("sbcl: %v", err)
	}
	wantLines, gotLines := strings.Split(want.String(), "\n"), strings.Split(string(out), "\n")
	if len(wantLines) != len(gotLines) {
		t.Fatalf("%d lines, want %d", len(gotLines), len(wantLines))
	}
	failures := 0
	for i := range wantLines {
		if wantLines[i] != gotLines[i] {
			failures++
			if failures <= 10 {
				t.Errorf("input %s\n go:   %s\n lisp: %s", strings.Split(file.String(), "\n")[i/3], wantLines[i], gotLines[i])
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d of %d lines differ", failures, len(wantLines))
	}
}
