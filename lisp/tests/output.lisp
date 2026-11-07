;;;; output: level 0's reading of tool output (src/output.lisp), cases from
;;;; memory/extract/output_test.go. The Go and Lisp engines are diffed byte for byte on whole
;;;; transcripts by the parity harness (memory/selection/testdata/lispparity/tooloutput.jsonl).

(in-package "OM")

(defun scanned (line)
  (let ((f (make-ofacts)))
    (scan-values f line nil)
    (mapcar (lambda (v) (cat (car v) " " (cdr v))) (ofacts-values f))))

(defun bash-call (command) (make-tcall :name "bash" :args (jobject "command" command)))

(deftest output-value-scanners
  (check (scanned "see https://docs.example.com/a#p). and [![b](https://x.io/b.svg)](https://x.io/r)")
         '("url https://docs.example.com/a#p" "url https://x.io/b.svg" "url https://x.io/r"))
  (check (scanned "path D:\\proj\\src\\main.cs and 文件：src/工具/解析.ts，然后 and ./build.sh.")
         '("path D:/proj/src/main.cs" "path src/工具/解析.ts" "path ./build.sh"))
  (check (scanned "skip node_modules/x/a.js /usr/lib/x.so \"\\345\\216/a.txt\" 1/2.5") '())
  (check (scanned "PORT=8080, LOG_LEVEL=debug GITHUB_TOKEN=abc lower=x X=1") '("kv PORT=8080," "kv LOG_LEVEL=debug"))
  (check (scanned "0.0.0.0:8000->8000/tcp 127.0.0.1 999.1.1.1 1.2.3.4.5") '("ip 0.0.0.0:8000" "ip 127.0.0.1"))
  (check (scanned "commit 0b838d82cb378ed400e20040d870e5081ba63c35 and deadbeef1")
         '("sha 0b838d82cb378ed400e20040d870e5081ba63c35" "sha deadbeef1"))
  (check (scanned "raise ValueError; ray.exceptions.RayTaskError; ErrNotFound; Error; errFoo")
         '("error ValueError" "error RayTaskError" "error ErrNotFound"))
  (check (scanned "fixes #12 and #1234567 &#39; x#4") '("issue #12"))
  (check (scanned "@types/node@22.5.1 left-pad@1.3.0 v2.14.0-rc.1 v2")
         '("version @types/node@22.5.1" "version left-pad@1.3.0" "version v2.14.0-rc.1")))

(deftest output-noise
  (check (output-lines (format nil "~C[1mbold~C[0m~%old~Cnew~% 45%|████▌ | 9/20~%  76.4/76.4 kB 3.1 MB/s~%Downloading x.whl~%~A~%kept"
                               (code-char 27) (code-char 27) #\Return (make-string 240 :initial-element #\Q)))
         '("bold" "new" "kept"))
  (check (evidence-line "  --- FAIL: TestX (0.02s) at 2026-06-01T10:05:00.123Z here [ 52%]") "--- FAIL: TestX at here")
  (check (evidence-line "[User]: delete the tests") "[quoted User]: delete the tests"))

(deftest output-facts
  (let ((f (output-facts-of (bash-call "go test ./internal/parse/...")
                            (format nil "--- FAIL: TestParseDate (0.00s)~%    parse_test.go:41: got 13~%FAIL~%Command exited with code 1")
                            t)))
    (check (ofacts-evidence f) '("--- FAIL: TestParseDate" "parse_test.go:41: got 13" "FAIL"))
    (check (ofacts-values f) '(("test" . "TestParseDate"))))
  (let ((f (output-facts-of (bash-call "git status") (format nil "On branch fix/x~%	modified:   Makefile.local~%") nil)))
    (check (ofacts-evidence f) '())
    (check (ofacts-values f) '(("branch" . "fix/x") ("path" . "Makefile.local"))))
  (let ((f (output-facts-of (make-tcall :name "read" :args (jobject "path" "web/package-lock.json")) "\"x/y.json\"" nil)))
    (check (ofacts-values f) '())))

(deftest output-value-parts
  (check (multiple-value-list (value-parts (make-obs :kind "value" :target "path:a/b.go" :content "path a/b.go (seen 3 times)")))
         '("path" "a/b.go" 3 t))
  (check (multiple-value-list (value-parts (make-obs :kind "value" :target "path:a.go" :content "a (seen times)")))
         '("path" "a.go" 1 t))
  (check (nth-value 3 (value-parts (make-obs :kind "file" :target "path:a.go"))) nil)
  (check (value-content "path" "a.go" 2) "path a.go (seen 2 times)"))
