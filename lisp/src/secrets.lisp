;;;; memory/secrets: the secret scanner (contracts/secrets.md). Each rule's keywords are looked
;;;; for in the ASCII-lowercased text; only then its pattern runs (over that lowercased text for a
;;;; folding rule), and each hit's value, checked against the placeholder allowlist and the rule's
;;;; entropy threshold, is replaced by [redacted:<rule>]. The rules apply in order, each to what
;;;; the ones before it left. Byte for byte what Go's secrets.Redact does (contracts/secrets.jsonl).

(in-package "OM.BASE")

(defstruct secret-rule id keywords regex fold groups generic (min-bits 0) (per-chars 1))

(defparameter +secret-entropy-chars+ 256)

(defparameter +secret-stopwords+
  '("example" "placeholder" "changeme" "change_me" "dummy" "redacted" "xxxx" "****" "your_" "your-"
    "<" ">" "{{" "${"))

(defun secret-placeholder (id) (concatenate 'string "[redacted:" id "]"))

(defun ascii-lower (s)
  "Lowercases ASCII letters only, so every character keeps its index."
  (if (find-if (lambda (c) (char<= #\A c #\Z)) s)
      (map 'string (lambda (c) (if (char<= #\A c #\Z) (char-downcase c) c)) s)
      s))

(defun secret-assignment (names least)
  (let ((n (format nil "~D" least)))
    (concatenate 'string
                 #r§\b([a-z0-9_.\-]{0,40}(?:§ names #r§)[a-z0-9_.\-]{0,40})§
                 #r§\\?["']?[ \t]*(?:=|:=|:|=>)[ \t]*§
                 #r§(?:\\?"([^"\\\n]{§ n #r§,256})\\?"|'([^'\\\n]{§ n #r§,256})'|\\?["']?([^\s"'\[\]<>{}(),;`$\\]{§ n #r§,}))§)))

(defparameter +secret-rules+
  (list
   (make-secret-rule :id "private-key" :keywords '("private key")
                     :regex (compile-regex #r§-----BEGIN[ A-Z0-9]{0,40}PRIVATE KEY(?: BLOCK)?-----(?:[\s\S]*?-----END[ A-Z0-9]{0,40}PRIVATE KEY(?: BLOCK)?-----|(?:(?:[ \t\r\n]|\\[nr])*[A-Za-z0-9+/=]{16,})*)§))
   (make-secret-rule :id "access-key-id" :keywords '("akia" "asia" "abia" "acca" "a3t")
                     :regex (compile-regex #r§\b((?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16})\b§) :groups '(1))
   (make-secret-rule :id "gh-token" :keywords '("ghp_" "gho_" "ghu_" "ghs_" "ghr_" "github_pat_")
                     :regex (compile-regex #r§\b((?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})§) :groups '(1))
   (make-secret-rule :id "gl-token" :keywords '("glpat-" "gldt-" "glrt-" "glptt-" "glsoat-")
                     :regex (compile-regex #r§\b((?:glpat|gldt|glrt|glptt|glsoat)-[A-Za-z0-9_\-]{20,})§) :groups '(1))
   (make-secret-rule :id "xox-token" :keywords '("xox")
                     :regex (compile-regex #r§\b(xox[abeoprs]-[A-Za-z0-9\-]{10,})§) :groups '(1))
   (make-secret-rule :id "webhook-url" :keywords '("hooks.slack.com" "/api/webhooks/")
                     :regex (compile-regex #r§(https://(?:hooks\.slack\.com/(?:services|workflows|triggers)/[A-Za-z0-9+/_\-]{8,}|discord(?:app)?\.com/api/webhooks/[0-9]+/[A-Za-z0-9_\-]{20,}))§) :groups '(1))
   (make-secret-rule :id "payment-key" :keywords '("sk_live_" "sk_test_" "rk_live_" "rk_test_" "whsec_")
                     :regex (compile-regex #r§\b((?:sk|rk)_(?:live|test)_[A-Za-z0-9]{10,}|whsec_[A-Za-z0-9]{20,})§) :groups '(1))
   (make-secret-rule :id "sk-key" :keywords '("sk-")
                     :regex (compile-regex #r§\b(sk-[A-Za-z0-9_\-]{20,})§) :groups '(1))
   (make-secret-rule :id "aiza-key" :keywords '("aiza")
                     :regex (compile-regex #r§\b(AIza[A-Za-z0-9_\-]{35,})§) :groups '(1))
   (make-secret-rule :id "jwt" :keywords '("eyj")
                     :regex (compile-regex #r§\b(eyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]*)§) :groups '(1))
   (make-secret-rule :id "url-credentials" :keywords '("://") :fold t :generic t :groups '(1)
                     :regex (compile-regex #r§\b[a-z][a-z0-9+.\-]{0,20}://[^\s:/@"'<>\[\]\\]{1,256}:([^\s/"'<>\[\]\\]{1,256})@§))
   (make-secret-rule :id "authorization" :keywords '("bearer" "basic" "token") :fold t :generic t :groups '(1)
                     :min-bits 3 :per-chars 1
                     :regex (compile-regex #r§\b(?:bearer|basic|token)[ \t]+([a-z0-9_\-.~+/]{16,}=*)§))
   (make-secret-rule :id "password-assignment" :keywords '("password" "passwd" "passphrase" "secret")
                     :fold t :generic t :groups '(2 3 4)
                     :regex (compile-regex (secret-assignment "password|passwd|passphrase|secret" 6)))
   (make-secret-rule :id "token-assignment" :keywords '("token" "key" "credential")
                     :fold t :generic t :groups '(2 3 4) :min-bits 7 :per-chars 2
                     :regex (compile-regex (secret-assignment #r§token|api[_\-]?key|access[_\-]?key|private[_\-]?key|auth[_\-]?key|session[_\-]?key|signing[_\-]?key|encryption[_\-]?key|credentials?§ 8)))))

(defun secret-prefilter-p (rule lower)
  (some (lambda (k) (search k lower)) (secret-rule-keywords rule)))

(defun secret-value-span (rule m)
  "The first of the rule's groups that took part in match m, or the whole match."
  (let ((groups (secret-rule-groups rule)))
    (if (null groups)
        (values (aref m 0) (aref m 1))
        (dolist (g groups (values -1 -1))
          (when (and (< (1+ (* 2 g)) (length m)) (>= (aref m (* 2 g)) 0))
            (return (values (aref m (* 2 g)) (aref m (1+ (* 2 g))))))))))

(defun entropy-at-least-p (value bits per)
  "Shannon entropy of value's first 256 characters at least bits/per bits per character, decided
exactly: n^(q*n) >= 2^(p*n) * prod(c^(q*c))."
  (let ((counts (make-hash-table)) (n 0))
    (loop for c across value
          while (< n +secret-entropy-chars+)
          do (incf (gethash c counts 0)) (incf n))
    (if (zerop n)
        (<= bits 0)
        (let ((left (expt n (* per n))) (right (ash 1 (* bits n))))
          (maphash (lambda (c k) (declare (ignore c)) (setf right (* right (expt k (* per k))))) counts)
          (>= left right)))))

(defun secret-accept-p (rule value)
  (and (plusp (length value))
       (not (search "[redacted:" value))
       (find-if (lambda (c) (char/= c (char value 0))) value)
       (or (not (secret-rule-generic rule))
           (let ((lv (ascii-lower value)))
             (and (not (and (plusp (length lv)) (char= (char lv 0) #\$)))
                  (notany (lambda (s) (search s lv)) +secret-stopwords+))))
       (or (zerop (secret-rule-min-bits rule))
           (entropy-at-least-p value (secret-rule-min-bits rule) (secret-rule-per-chars rule)))))

(defun scan-secrets (text)
  "The redacted text and a list of (rule start end) for each value replaced."
  (if (zerop (length text))
      (values text '())
      (let ((text (coerce text 'simple-string)) (lower nil) (findings '()))
        (setf lower (ascii-lower text))
        (dolist (rule +secret-rules+)
          (when (secret-prefilter-p rule lower)
            (let ((matches (regex-find-all-index (secret-rule-regex rule) (if (secret-rule-fold rule) lower text))))
              (when matches
                (let ((last 0))
                  (let ((out (with-output-to-string (o)
                               (dolist (m matches)
                                 (multiple-value-bind (start end) (secret-value-span rule m)
                                   (when (and (>= start 0) (secret-accept-p rule (subseq text start end)))
                                     (write-string text o :start last :end start)
                                     (write-string (secret-placeholder (secret-rule-id rule)) o)
                                     (push (list (secret-rule-id rule) start end) findings)
                                     (setf last end))))
                               (write-string text o :start last))))
                    (when (plusp last)
                      (setf text (coerce out 'simple-string) lower (ascii-lower text)))))))))
        (values text (nreverse findings)))))

(defun redact-secrets (text)
  "secrets.Redact."
  (values (scan-secrets text)))

(defun contains-secret-p (text)
  (and (nth-value 1 (scan-secrets text)) t))

(defun redact-json-text (text)
  "secrets.RedactJSON: every string value of a JSON text redacted (member names kept), every other
character as it was; a value that changes is written as encoding/json writes a string."
  (let ((lower (ascii-lower text)))
    (if (notany (lambda (rule) (secret-prefilter-p rule lower)) +secret-rules+)
        text
        (let ((n (length text)) (last 0) (i 0))
          (let ((out
                  (with-output-to-string (o)
                    (loop while (< i n)
                          do (if (char/= (char text i) #\")
                                 (incf i)
                                 (let ((end (let ((j (1+ i)))
                                              (loop (when (>= j n) (return nil))
                                                    (case (char text j)
                                                      (#\\ (incf j 2))
                                                      (#\" (return (1+ j)))
                                                      (t (incf j)))))))
                                   (unless end (return))
                                   (unless (let ((j end))
                                             (loop while (and (< j n) (member (char text j) '(#\Space #\Tab #\Newline #\Return)))
                                                   do (incf j))
                                             (and (< j n) (char= (char text j) #\:)))
                                     (let ((value (ignore-errors (json-parse (subseq text i end)))))
                                       (when (stringp value)
                                         (let ((redacted (redact-secrets value)))
                                           (when (string/= redacted value)
                                             (write-string text o :start last :end i)
                                             (write-string (json-string redacted) o)
                                             (setf last end))))))
                                   (setf i end))))
                    (write-string text o :start last))))
            (if (zerop last) text out))))))
