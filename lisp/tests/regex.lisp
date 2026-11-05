;;;; The matcher against RE2's semantics as Go's regexp package documents and behaves.

(in-package "OM")

(defun find-all (pattern text) (regex-find-all-strings (compile-regex pattern) text))

(deftest regex-leftmost-first
  ;; RE2's leftmost-first: the first alternative that matches wins, not the longest.
  (check (regex-find-string (compile-regex "a|ab") "ab") "a")
  (check (regex-find-string (compile-regex "ab|a") "ab") "ab")
  (check (regex-find-submatch (compile-regex #r§(a+?)(a*)§) "aaa") '("aaa" "a" "aa"))
  (check (length (find-all "(?i)k" (cat "kK" (string (code-char #x212a))))) 3)
  (check (regex-find-submatch (compile-regex #r§(?i)\bnot\s+(.+?),?\s+but\b§) "It is Not Redis, but Memcached")
         '("Not Redis, but" "Redis")))

(deftest regex-empty-matches
  ;; FindAll: an empty match abutting the previous match is dropped, and the search moves on.
  (check (mapcar (lambda (m) (coerce (subseq m 0 2) 'list)) (regex-find-all-index (compile-regex "a*") "baaac"))
         '((0 0) (1 4) (5 5)))
  (check (regex-replace-all (compile-regex "a*") "baaac" "-") "-b-c-")
  (check (regex-replace-all (compile-regex "x*") "abc" "-") "-a-b-c-"))

(deftest regex-ascii-classes
  ;; \d, \w, \s and \b are ASCII in RE2.
  (check (find-all #r§\d+§ "12 ٣٤ 5") '("12" "5"))
  (check (find-all #r§\w+§ "café au lait") '("caf" "au" "lait"))
  (check (find-all #r§\bau\b§ "éau au") '("au" "au"))
  (check (regex-match-p (compile-regex #r§^\s$§) (string (code-char 11))) nil)
  (check (find-all #r§[\p{L}\p{N}]+§ "Zoë mentioned Øresund ٣") '("Zoë" "mentioned" "Øresund" "٣")))

(deftest regex-anchors-and-flags
  (check (regex-match-p (compile-regex "^b") "a
b") nil)
  (check (regex-match-p (compile-regex "(?m)^b") "a
b") t)
  ;; $ without (?m) is the end of the text, not before a final newline.
  (check (regex-match-p (compile-regex "a$") "a
") nil)
  (check (find-all "(?i)straße" "STRAßE strasse") '("STRAßE"))
  (check (regex-find-submatch (compile-regex #r§(?m)^\[[^\]\s]+(?: \([^)]*\))? ([0-9a-f]{7,40})\] (.+)$§)
                              "Running hooks
[main (root-commit) 0123abc] Add it
 1 file changed")
         '("[main (root-commit) 0123abc] Add it" "0123abc" "Add it")))

(deftest regex-repeats
  (check (find-all #r§x{2,3}§ "xxxxxxx") '("xxx" "xxx"))
  (check (find-all #r§x{2}§ "xxxxx") '("xx" "xx"))
  (check (find-all #r§a{,2}§ "a{,2}") '("a{,2}"))
  (check (find-all #r§(?:ab)+?c§ "ababc") '("ababc"))
  ;; A lazy match over a long text stays shallow.
  (let ((long (make-string 200000 :initial-element #\x)))
    (check (length (regex-replace-all (compile-regex #r§<f>[\s\S]*?</f>§) (cat "<f>" long "</f>!") "")) 1)))

(deftest regex-named-groups-and-templates
  (let ((re (compile-regex #r§(?P<day>\d+)-(?P<month>\d+)§)))
    (check (coerce (regex-names re) 'list) '("" "day" "month"))
    (check (regex-replace-all re "5-6 and 7-8" "${month}/$day") "6/5 and 8/7"))
  (check (regex-replace-all (compile-regex #r§(\d),(\d)§) "1,234,567" "$1$2") "1234567"))
