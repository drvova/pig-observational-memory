package contracts

// The security-weakening pattern list's parity check (contracts/insecure.md): random texts built
// from fragments of the rules, their negations and clause marks are matched by the Go engine and
// by the Lisp core (lisp/insecure-parity.lisp), and the rules named must be equal. Like the
// other Lisp checks, it runs only with OM_LISP_PARITY=1 and sbcl on PATH.

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/insecure"
)

var insecureFragments = []string{
	"verify=False", "verify = false", "verify=True", "InsecureSkipVerify", "NODE_TLS_REJECT_UNAUTHORIZED=0", "rejectUnauthorized: false",
	"curl", " -k", " --insecure", "--insecure-skip-tls-verify", "--no-check-certificate", "http.sslVerify false", "strict-ssl false",
	"check_hostname = False", "CERT_NONE", "disable ", "skip ", "turn off ", "ignore ", "the ", "TLS ", "certificate ", "verification",
	"checks", "errors", "CSRF ", "auth ", "protection", "@csrf_exempt", "Access-Control-Allow-Origin: *", "chmod 777", "chmod -R a+rwx",
	"chmod 755", "--no-verify", "HUSKY=0", "pre-commit hooks", "--no-sandbox", "--dangerously-skip-permissions", "sandbox", "setenforce 0",
	"--privileged", "--allow-unauthenticated", "gpgcheck=0", "--trusted-host", "signature verification", "store the passwords in plain text",
	"hardcode the API key", "eval(", "shell=True", " | sh", " | sudo bash", " | jq", "wget -qO- x", "Math.random() for tokens", "md5 for passwords",
	"never ", "don't ", "don’t ", "do not ", "not ", "Never mind", "avoid ", "always ", "please ", "just ", "use ", "prefer ",
	", ", ". ", ": ", "; ", "!", "?", "\n", "\t", " ", "e.g.", "a.b", "x:y", "K", "ſ", "ÄöÜ", "漢字",
}

func TestLispInsecureParity(t *testing.T) {
	if os.Getenv("OM_LISP_PARITY") != "1" {
		t.Skip("set OM_LISP_PARITY=1 to diff the Go engine against the SBCL port in lisp/")
	}
	sbcl, err := exec.LookPath("sbcl")
	if err != nil {
		t.Skip("sbcl is not on PATH")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "lisp", "insecure-parity.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(19, 23))
	var inputs []string
	for range 3000 {
		var b strings.Builder
		for k := rng.IntN(10) + 1; k > 0; k-- {
			b.WriteString(insecureFragments[rng.IntN(len(insecureFragments))])
		}
		inputs = append(inputs, b.String())
	}
	var file, want strings.Builder
	held := 0
	for _, in := range inputs {
		line, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		file.Write(line)
		file.WriteByte('\n')
		rule, ok := insecure.Match(in)
		if ok {
			held++
		}
		want.WriteString(rule + "\n")
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
				t.Errorf("input %q\n go:   %q\n lisp: %q", inputs[i], wantLines[i], gotLines[i])
			}
		}
	}
	if failures > 0 {
		t.Fatalf("%d of %d inputs differ", failures, len(inputs))
	}
	t.Logf("identical: %d inputs, %d held", len(inputs), held)
	if held < 300 || held > 2900 {
		t.Errorf("%d of %d inputs held: the fragments no longer reach both answers", held, len(inputs))
	}
}
