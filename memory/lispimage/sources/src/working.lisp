;;;; Phase 3, working memory (memory/ledger/working.go, memory/extract/receipts.go and the rule
;;;; replacements of memory/extract/conversation.go): closing links on tool receipts, the plan
;;;; keys a receipt and a newer request are matched on, and a user rule replacing the one it
;;;; names. Byte for byte as Go (contracts/encoding.jsonl, lisp/tests/working.lisp).

(in-package "OM.ENGINE")

;;; ledger/working.go

(defun closed-by (all)
  "ClosedBy: the closing link naming each plan first, in ledger order, by plan id."
  (let ((out (make-set)))
    (dolist (o all out)
      (dolist (c (obs-closes o))
        (unless (gethash (first c) out) (setf (gethash (first c) out) c))))))

(defun plan-keys (o)
  "PlanKeys: the keys of a plan's text, a leading ./ taken off."
  (let ((out '()))
    (dolist (k (record-keys (obs-content o)) out)
      (let ((k (trim-prefix k "./")))
        (when (and (string/= k "") (not (member-str k out))) (push-end k out))))))

(defun path-matches-p (k r)
  (or (string= k r)
      (multiple-value-bind (short long) (if (> (length k) (length r)) (values r k) (values k r))
        (and (contains-any short "/.") (has-suffix long (cat "/" short))))))

(defun key-matches-p (k r)
  "KeyMatches: a receipt's key r naming a plan's key k."
  (let ((k (trim-prefix k "./")) (r (trim-prefix r "./")))
    (cond ((or (string= k "") (string= r "")) nil)
          ((path-matches-p k r) t)
          ((not (find #\Space r)) nil)
          ((contains (cat " " r " ") (cat " " k " ")) t)
          (t (some (lambda (w) (path-matches-p k (trim-prefix w "./"))) (fields r))))))

(defun working-word-char-p (c)
  (or (char<= #\a c #\z) (char<= #\A c #\Z) (char<= #\0 c #\9) (char= c #\-) (char= c #\_)))

(defun words-of (text)
  "wordsOf: runs of ASCII letters, digits, - and _."
  (fields-func text (lambda (c) (not (working-word-char-p c)))))

(defun read-plan-p (o)
  "IsReadPlan: the plan's text asks to read, reread or look at something."
  (let ((words (words-of (to-lower (obs-content o)))))
    (loop for (w . rest) on words
          thereis (or (member-str w '("read" "reread" "re-read"))
                      (and (string= w "look") rest (string= (first rest) "at"))))))

(defun stale-terms (o)
  "StaleTerms: the plan's keys and, for a path key, its last element without the extension when
at least four characters long."
  (let ((out '()))
    (flet ((add (term) (when (and (string/= term "") (not (member-str term out))) (push-end term out))))
      (dolist (k (plan-keys o))
        (add k)
        (when (or (find #\/ k) (find #\. k))
          (let* ((slash (position #\/ k :from-end t))
                 (base (if slash (subseq k (1+ slash)) k))
                 (dot (position #\. base :from-end t)))
            (when (and dot (> dot 0)) (setf base (subseq base 0 dot)))
            (when (and (>= (length base) 4) (string/= base k)) (add base))))))
    out))

(defun bears-on-p (text terms)
  "BearsOn: a request's text names one of terms, as a key of its text or a whole word."
  (let ((keys (record-keys text)) (words :unread))
    (dolist (term terms nil)
      (when (some (lambda (k) (key-matches-p term (trim-prefix k "./"))) keys)
        (return t))
      (unless (contains-any term " /.`")
        (when (eq words :unread) (setf words (words-of (to-lower text))))
        (when (member-str (to-lower term) words) (return t))))))

;;; extract/receipts.go

(defparameter +unique-line-min+ 8)

(defstruct (receipt (:conc-name receipt-)) kind (keys '()) (target "") (output ""))

(defun scope-exclusion-p (text)
  "IsScopeExclusion: a scope exclusion's wording (the scope patterns)."
  (let ((text (replace-all text "’" "'")))
    (some (lambda (p) (regex-match-p p text)) +scope-patterns+)))

(defun question-text-p (text) (question-p (trim-space text)))

(defun call-receipt (call key result-text)
  "callReceipt: exit 0 for a shell command (keys: the command and its call key), read for a call
whose tool reads a file."
  (let ((command (shell-command (tcall-args call))))
    (cond (command (make-receipt :kind "exit 0" :keys (if (string/= key command) (list command key) (list command))))
          ((equal (file-tool-state call) "read")
           (make-receipt :kind "read" :keys (list key) :target key :output result-text))
          (t nil))))

(defun standing-plans (x)
  "standingPlans: (values ledger-plans own-plan-records)."
  (let ((superseded (make-set)) (closed (closed-by (folded-observations (x-ledger x)))) (plans '()) (own '()))
    (maphash (lambda (id v) (when v (setf (gethash id superseded) t))) (x-superseded x))
    (dolist (r (x-records x))
      (dolist (id (obs-supersedes (rec-obs r))) (setf (gethash id superseded) t)))
    (dolist (o (folded-active (x-ledger x)))
      (when (and (string= (obs-status o) "proposed") (not (gethash (obs-id o) superseded)) (not (gethash (obs-id o) closed)))
        (push-end o plans)))
    (dolist (r (x-records x))
      (when (and (string= (obs-status (rec-obs r)) "proposed") (not (gethash (obs-id (rec-obs r)) superseded)))
        (push-end r own)))
    (values plans own)))

(defun unique-line-p (x rc)
  "uniqueLine: the read's output holds a line, trimmed, at least 8 bytes long with a letter, that no
output of a call on another target holds."
  (let ((others (make-set)))
    (dolist (o (x-outputs x))
      (unless (string= (car o) (receipt-target rc))
        (dolist (line (split (cdr o) (string #\Newline)))
          (setf (gethash (trim-space line) others) t))))
    (dolist (line (split (receipt-output rc) (string #\Newline)) nil)
      (let ((line (trim-space line)))
        (when (and (>= (length (sb-ext:string-to-octets line :external-format :utf-8)) +unique-line-min+)
                   (contains-any (to-lower line) "abcdefghijklmnopqrstuvwxyz")
                   (not (gethash line others)))
          (return t))))))

(defun closes-plan-p (x plan rc)
  "closesPlan."
  (unless (eq (and (read-plan-p plan) t) (string= (receipt-kind rc) "read"))
    (return-from closes-plan-p nil))
  (let ((matched (some (lambda (k) (some (lambda (r) (key-matches-p k r)) (receipt-keys rc))) (plan-keys plan))))
    (if (or (not matched) (string/= (receipt-kind rc) "read"))
        (and matched t)
        (unique-line-p x rc))))

(defun would-close-p (x rc anchor)
  "wouldClose."
  (when rc
    (multiple-value-bind (plans own) (standing-plans x)
      (or (some (lambda (p) (closes-plan-p x p rc)) plans)
          (some (lambda (r) (and (< (rec-anchor r) anchor) (closes-plan-p x (rec-obs r) rc))) own)))))

(defun x-resolve-closures (x)
  "resolveClosures."
  (multiple-value-bind (plans own) (standing-plans x)
    (let ((closed (make-set)))
      (dolist (r (x-records x))
        (let ((rc (rec-receipt r)))
          (when rc
            (flet ((try (plan)
                     (when (and (not (gethash (obs-id plan) closed)) (string/= (obs-id plan) (obs-id (rec-obs r)))
                                (closes-plan-p x plan rc))
                       (setf (gethash (obs-id plan) closed) t)
                       (push-end (list (obs-id plan) (obs-id (rec-obs r)) (receipt-kind rc)) (obs-closes (rec-obs r))))))
              (dolist (p plans) (try p))
              (dolist (p own) (when (< (rec-anchor p) (rec-anchor r)) (try (rec-obs p)))))))))))

(defun requests-of (entries end)
  "Requests: by index below end, the index of the newest request at or before it, -1 before any."
  (let* ((end (min end (length entries))) (out (make-array end :initial-element -1)) (current -1))
    (dotimes (i end out)
      (when (request-entry-p (aref entries i)) (setf current i))
      (setf (aref out i) current))))

(defun request-text (e)
  "RequestText: a user message's text, harness markup removed."
  (if (entry-message e) (without-markup (text-blocks (jget (entry-message e) "content"))) ""))

;;; extract/conversation.go: rule replacements

(defun replaced-rule-phrase (sentence)
  "replacedRulePhrase: the phrase a rule names as what it replaces, holding a key; \"\" for none."
  (let ((sentence (replace-all sentence "’" "'")))
    (dolist (pattern (list +correction-not-but+ +correction-instead-x+ +correction-not-tail+) "")
      (dolist (m (regex-find-all-submatch pattern sentence))
        (let ((phrase (trim-space (second m))))
          (when (record-keys phrase) (return-from replaced-rule-phrase phrase)))))))

(defun x-resolve-rule-replacements (x)
  "resolveRuleReplacements."
  (let ((earlier '()))
    (dolist (o (folded-active (x-ledger x)))
      (when (and (string= (obs-kind o) "preference") (user-priority-p o) (string= (obs-status o) "")
                 (not (gethash (obs-id o) (x-superseded x))))
        (push-end (cons (obs-id o) (obs-content o)) earlier)))
    (dolist (r (x-records x))
      (let ((o (rec-obs r)))
        (when (and (string= (obs-kind o) "preference") (string= (obs-status o) ""))
          (when (string/= (rec-replaces-rule r) "")
            (let ((keys (record-keys (rec-replaces-rule r))))
              (dolist (c earlier)
                (unless (or (gethash (car c) (x-superseded x)) (string= (car c) (obs-id o)))
                  (let ((held (record-keys (cdr c))))
                    (when (every (lambda (k) (member-str k held)) keys)
                      (push-end (car c) (obs-supersedes o))
                      (setf (gethash (car c) (x-superseded x)) t)))))))
          (push-end (cons (obs-id o) (obs-content o)) earlier))))))
