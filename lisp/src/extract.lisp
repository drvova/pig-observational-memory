;;;; memory/extract: level-0 extraction (extract.go, conversation.go, text.go, work.go,
;;;; shell.go, drop.go). The memory tool's notes (work.go note), which parse the code tool's
;;;; details and the frozen legacy query language, are src/legacy/notes.lisp's x-memory-note.

(in-package "OM.ENGINE")

(defparameter +code-relevance+ "medium")
(defparameter +memory-tool+ "memory")
(defparameter +max-record-content-chars+ 10000)
(defparameter +brief-key-arg-runes+ 80)

;;; serialize and tokens

(defun truncate-record-content (content)
  "serialize.TruncateRecordContent."
  (let ((length (utf16-len content)))
    (if (<= length +max-record-content-chars+)
        content
        (cat (utf16-head content +max-record-content-chars+)
             " … [truncated " (itoa (- length +max-record-content-chars+)) " chars]"))))

(defun observation-line-tokens (id timestamp relevance content)
  "tokens.ObservationLineTokenCount."
  (estimate-tokens (cat "[" id "] " timestamp " [" relevance "] " content)))

(defregex +skill-block+ #r§^<skill name="[^"]+" location="[^"]+">\n[\s\S]*?\n</skill>(?:\n\n([\s\S]+))?$§)
(defparameter +markup-wrappers+
  (mapcar (lambda (tag) (compile-regex (cat "<" tag #r§(?:\s[^>]*)?>[\s\S]*?</§ tag ">")))
          '("file" "system-reminder" "ide_opened_file" "command-message" "command-name" "command-args"
            "local-command-stdout" "context-window-usage")))

(defun without-markup (text)
  "serialize.WithoutMarkup."
  (let ((m (regex-find-submatch +skill-block+ (trim-space text))))
    (when m (setf text (second m))))
  (dolist (re +markup-wrappers+)
    (setf text (regex-replace-all re text "")))
  (trim-space text))

;;; Paths (path/filepath on Unix)

(defun path-clean (path)
  "filepath.Clean."
  (if (string= path "")
      "."
      (let* ((rooted (char= (char path 0) #\/))
             (out '()))
        (dolist (part (split path "/"))
          (cond ((or (string= part "") (string= part ".")))
                ((string= part "..")
                 (cond ((and out (string/= (first out) "..")) (pop out))
                       ((not rooted) (push ".." out))))
                (t (push part out))))
        (let ((body (join (reverse out) "/")))
          (cond (rooted (cat "/" body))
                ((string= body "") ".")
                (t body))))))

(defun path-abs-p (p) (has-prefix p "/"))

(defun path-join (a b)
  (path-clean (cond ((string= a "") b) ((string= b "") a) (t (cat a "/" b)))))

(defun path-rel (basepath targpath)
  "filepath.Rel: the relative path, or nil on error."
  (let ((base (path-clean basepath)) (targ (path-clean targpath)))
    (when (string= targ base) (return-from path-rel "."))
    (when (string= base ".") (setf base ""))
    (unless (eq (and (plusp (length base)) (char= (char base 0) #\/))
                (and (plusp (length targ)) (char= (char targ 0) #\/)))
      (return-from path-rel nil))
    (let ((bl (length base)) (tl (length targ)) (b0 0) (bi 0) (t0 0) (ti 0))
      (loop
        (loop while (and (< bi bl) (char/= (char base bi) #\/)) do (incf bi))
        (loop while (and (< ti tl) (char/= (char targ ti) #\/)) do (incf ti))
        (unless (string= (subseq targ t0 ti) (subseq base b0 bi)) (return))
        (when (< bi bl) (incf bi))
        (when (< ti tl) (incf ti))
        (setf b0 bi t0 ti))
      (when (string= (subseq base b0 bi) "..") (return-from path-rel nil))
      (if (/= b0 bl)
          (let ((seps (count #\/ (subseq base b0 bl))))
            (with-output-to-string (out)
              (write-string ".." out)
              (dotimes (i seps) (write-string "/.." out))
              (when (/= t0 tl) (write-char #\/ out) (write-string targ out :start t0))))
          (subseq targ t0)))))

;;; State

(defstruct ctarget (phrase ""))

(defstruct rec
  obs
  (anchor -1)
  (exchange -1)
  correction
  retraction
  (named-phrase "")
  (named-words "")
  fallback
  (fallback-status "")
  undated)

(defstruct (xstate (:conc-name x-))
  entries
  (start 0) (end 0)
  skip-tools
  ledger
  (cwd "")
  (file-ops '())
  (file-ops-from 0)
  (records '())
  (superseded (make-set))
  (unresolved (make-set))
  (words (make-set))
  (files (make-set))
  (file-events (make-set))
  (file-order '())
  (notes (make-set))
  (subjects '())
  known-given
  (known (make-set))
  (exchange -1)
  (exchange-texts '()))

(defstruct batch observations covers)

(defun entry-at (x i) (aref (x-entries x) i))

(defun entry-time (e)
  (if (string/= (entry-timestamp e) "")
      (entry-timestamp e)
      (and (entry-message e) (jget (entry-message e) "timestamp"))))

(defun anchor-day (e)
  "anchorTime as a day count, nil when unreadable."
  (let ((ms (parse-timestamp (entry-time e))))
    (and ms (millis-day ms))))

(defun normalized (text) (to-lower (one-line text)))

(defun x-add (x o &rest sources)
  (let ((anchor -1) (n (length (x-entries x))))
    (dolist (i sources)
      (when (and (>= i 0) (< i n))
        (setf anchor (max anchor i))
        (let ((id (entry-id (entry-at x i))))
          (when (and (string/= id "") (not (member-str id (obs-source-ids o))))
            (push-end id (obs-source-ids o))))))
    ;; Secrets are redacted before the content is cut (contracts/secrets.md).
    (setf (obs-content o) (truncate-record-content (redact-secrets (obs-content o)))
          (obs-target o) (redact-secrets (obs-target o))
          (obs-fields o) (mapcar (lambda (f) (cons (car f) (redact-secrets (cdr f)))) (obs-fields o)))
    (when (and (string= (obs-timestamp o) "") (>= anchor 0))
      (setf (obs-timestamp o) (format-timestamp (entry-time (entry-at x anchor)))))
    (setf (obs-relevance o) +code-relevance+)
    (when (string= (obs-origin o) "") (setf (obs-origin o) "code"))
    (let ((r (make-rec :obs o :anchor anchor :exchange (x-exchange x))))
      (push-end r (x-records x))
      r)))

(defun record-id (o)
  (hash-id (cat (obs-content o) (string (code-char 0)) (join (obs-source-ids o) ","))))

(defun attempt-key (tool target) (cat tool (string (code-char 0)) target))

(defun attempt-words (o)
  (if (or (not (has-suffix (obs-content o) "]")) (string= (obs-tool o) ""))
      ""
      (let ((i (last-index (obs-content o) (cat " [" (obs-tool o) " \""))))
        (if (and i (> i 0)) (subseq (obs-content o) 0 i) ""))))

(defun load-ledger (x)
  (let* ((ledger (x-ledger x)) (active (folded-active ledger)) (contents '()))
    (dolist (o (folded-observations ledger))
      (dolist (id (obs-supersedes o)) (setf (gethash id (x-superseded x)) t)))
    (dolist (o active)
      (push (obs-content o) contents)
      (setf (gethash (normalized (obs-content o)) (x-known x)) t)
      (unless (gethash (obs-id o) (x-superseded x))
        (cond ((string= (obs-kind o) "attempt")
               (when (member (obs-outcome o) '("failed" "open") :test #'string=)
                 (let ((key (attempt-key (obs-tool o) (obs-target o))))
                   (push-end (obs-id o) (gethash key (x-unresolved x)))
                   (setf (gethash (obs-id o) (x-words x)) (attempt-words o)))))
              ((string= (obs-kind o) "file") (push-end (obs-id o) (gethash (obs-target o) (x-files x))))
              ((string= (obs-kind o) "note") (push-end (obs-id o) (gethash (obs-target o) (x-notes x)))))))
    (setf (x-subjects x) (mapcar #'obs-subject active))
    (setf (x-known-given x) (new-given (nreverse contents)))))

;;; text.go

(defun text-blocks (content)
  (cond ((stringp content) content)
        ((vectorp content)
         (join (loop for b across content
                     when (and (hash-table-p b) (equal (jget b "type") "text") (stringp (jget b "text")))
                       collect (jget b "text"))
               (string #\Newline)))
        (t "")))

(defregex +markup-only+ #r§^</?[A-Za-z][\w-]*(?:\s[^<>]*)?/?>$§)
(defregex +list-marker+ #r§^(?:[-*+•]|\d{1,3}[.)])\s+§)
(defregex +bare-marker+ #r§^(?:[-*+•]|\d{1,3}[.)])$§)
(defregex +block-prefix+ #r§^(?:>\s*)+|^#{1,6}\s+§)
(defregex +thematic-break+ #r§^(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$§)
(defregex +numbered-marker+ #r§^(\d{1,3})[.)]§)
(defregex +trailing-marker+ #r§\s(\d{1,3})\.$§)

(defun markup-only-p (sentence) (regex-match-p +markup-only+ (trim-space sentence)))

(defun clip (text n)
  (let ((line (one-line text)))
    (cond ((<= (length line) n) line)
          ((<= n 1) "…")
          (t (cat (subseq line 0 (1- n)) "…")))))

(defstruct unit (sentences '()) (items '()))

(defun split-line (line)
  "splitLine: a line split at its sentence ends."
  (let ((pieces '()) (start 0) (n (length line)) (i 0))
    (loop while (< i n)
          do (when (find (char line i) ".!?")
               (let ((j (1+ i)))
                 (loop while (and (< j n) (find (char line j) ".!?\"')")) do (incf j))
                 (let ((k j))
                   (loop while (and (< k n) (member (char line k) '(#\Space #\Tab))) do (incf k))
                   (unless (or (= k j) (>= k n))
                     (let ((r (char line k)))
                       (when (or (upper-p r) (digit-p r) (find r "\"'(["))
                         (push (subseq line start j) pieces)
                         (setf start k i (1- k))))))))
             (incf i))
    (push (subseq line start) pieces)
    (setf pieces (nreverse pieces))
    (let ((out '()) (pending ""))
      (dolist (piece pieces)
        (if (regex-match-p +bare-marker+ (trim-space piece))
            (setf pending (cat pending piece " "))
            (progn (push (cat pending piece) out) (setf pending ""))))
      (let ((out (coerce (nreverse out) 'simple-vector)))
        (loop for k from 0 below (1- (length out))
              do (let ((m (regex-find-submatch-index +trailing-marker+ (aref out k))))
                   (when m
                     (let* ((piece (aref out k))
                            (number (parse-integer piece :start (aref m 2) :end (aref m 3)))
                            (next (cat (itoa (1+ number)) ".")))
                       (when (loop for later from (1+ k) below (length out)
                                   thereis (has-prefix (aref out later) (cat next " ")))
                         (setf (aref out (1+ k)) (cat (subseq piece (aref m 2) (aref m 1)) " " (aref out (1+ k))))
                         (setf (aref out k) (trim-right (subseq piece 0 (aref m 0)) (format nil " ~C" #\Tab))))))))
        (coerce out 'list)))))

(defun units (text)
  (let ((out '()) (fenced nil) (blank nil) (list nil) (numbered nil) (last-number 0))
    (flet ((close-list () (when list (push list out) (setf list nil))))
      (dolist (line (split text (string #\Newline)))
        (block continue
          (let* ((trimmed (trim-space line))
                 (indented (string/= line (trim-left line (format nil " ~C" #\Tab)))))
            (when (or (has-prefix trimmed "```") (has-prefix trimmed "~~~"))
              (setf fenced (not fenced))
              (when (or (null list) (not indented)) (close-list))
              (return-from continue))
            (when fenced (return-from continue))
            (when (string= trimmed "") (setf blank t) (return-from continue))
            (let ((after-blank blank))
              (setf blank nil)
              (setf trimmed (trim-space (regex-replace-all +block-prefix+ trimmed "")))
              (when (regex-match-p +thematic-break+ trimmed) (return-from continue))
              (let ((m (regex-find-string +list-marker+ trimmed)))
                (when (string/= m "")
                  (let ((item (one-line (subseq trimmed (length m)))))
                    (when (and (string/= item "") (not (regex-match-p +bare-marker+ item)))
                      (let ((number 0) (is-number nil))
                        (let ((nm (regex-find-submatch +numbered-marker+ m)))
                          (when nm (setf number (parse-integer (second nm)) is-number t)))
                        (when (and list after-blank (or (not (eq numbered is-number)) (and is-number (<= number last-number))))
                          (close-list))
                        (unless list (setf list (make-unit)))
                        (push-end item (unit-items list))
                        (setf numbered is-number last-number number))))
                  (return-from continue)))
              (when (regex-match-p +bare-marker+ trimmed) (return-from continue))
              (when (and list indented)
                (let ((items (unit-items list)))
                  (setf (car (last items)) (cat (car (last items)) " " (one-line trimmed))))
                (return-from continue))
              (close-list)
              (let ((u (make-unit)))
                (dolist (s (split-line trimmed))
                  (let ((s (one-line s)))
                    (unless (string= s "") (push-end s (unit-sentences u)))))
                (when (unit-sentences u) (push u out)))))))
      (close-list))
    (nreverse out)))

(defun sentences (text)
  (let ((out '()))
    (dolist (u (units text) (nreverse out))
      (if (null (unit-items u))
          (dolist (s (unit-sentences u)) (push s out))
          (dolist (item (unit-items u))
            (dolist (s (split-line item))
              (let ((s (one-line s)))
                (unless (string= s "") (push s out)))))))))

(defparameter +ack-words+
  (let ((h (make-set)))
    (dolist (w '("ok" "okay" "k" "yes" "yeah" "yep" "no" "nope" "thanks" "thank" "you" "thx" "sure" "great"
                 "good" "got" "it" "sounds" "cool" "nice" "perfect" "right" "alright" "done" "fine" "awesome"
                 "excellent" "so" "much" "please" "go" "ahead" "continue" "proceed" "lgtm" "agreed" "actually"
                 "correct" "exactly" "understood" "noted" "hi" "hello")
             h)
      (setf (gethash w h) t))))

(defun bare-ack-p (sentence)
  (let ((words (fields-func (to-lower sentence)
                            (lambda (r) (and (not (letter-p r)) (not (digit-p r)) (char/= r #\'))))))
    (every (lambda (w) (gethash (trim w "'") +ack-words+)) words)))

(defun question-p (sentence)
  (has-suffix (trim-right sentence "\"') ") "?"))

;;; conversation.go

(defregex +correction-marker+ #r§(?i)\b(?:actually|correction)\b§)
(defregex +correction-not-but+ #r§(?i)\bnot\s+(.+?),?\s+but\b§)
(defregex +correction-instead-x+ #r§(?i)\binstead\s+of\s+(.+?)(?:[,.;:!?]|$)§)
(defregex +additive+ #r§(?i)^(?:only|just|merely|simply|solely)\b§)
(defregex +correction-not-tail+ #r§(?i),\s*not\s+([^,.;:!?]+)[.!]?$§)

(defparameter +preference-patterns+
  (mapcar #'compile-regex
          (list #r§(?i)\bprefer(?:s|red|ring)?\s+\w§
                #r§(?i)\b(?:don'?t|do not)\s+want\b§
                #r§(?i)\balways\s+(?:use|do|run|prefer|keep|make|format|write|add|set|put|prefix|start|include|append)\b§
                #r§(?i)\bnever\s+(?:use|do|run|push|commit|write|ignore|add|set|put|remove|delete|include|deploy)\b§
                #r§(?i)\bplease\s+(?:use|avoid|keep|make|don'?t|do not|format|write)\b§
                #r§(?i)\b(?:style|format|language|naming)\s*[:=]\s*\S§)))

(defparameter +self-statements+
  (mapcar #'compile-regex
          (list #r§(?i)\bi\s+(?:really\s+|also\s+|just\s+|absolutely\s+)?(?:like|love|enjoy|hate|dislike|adore)\b§
                #r§(?i)\b(?:i'?m|i\s+am)\s+(?:really\s+|also\s+)?(?:into|a\s+(?:big\s+|huge\s+)?fan\s+of|interested\s+in|passionate\s+about|allergic\s+to)\b§
                #r§(?i)\bmy\s+favou?rite\b§
                #r§(?i)\bi\s+(?:usually|often|always|normally|regularly|typically)\s+\w§
                #r§(?i)\b(?:i'?ve|i\s+have)\s+been\s+\w+ing\b§
                #r§(?i)\b(?:i\s+work|i'?m\s+working|i\s+am\s+working)\s+(?:in|as|at|on|with|from)\b§)))

(defregex +hypothetical-wording+ #r§(?i)\b(?:if\s+(?:we|i|you)|suppose|supposing|what\s+if|hypothetically)\b§)
(defregex +planned-wording+ #r§(?i)\b(?:let'?s|let\s+us|we\s+will|we'll|we\s+are\s+going\s+to|we're\s+going\s+to|i\s+will|i'll|i'm\s+going\s+to|i\s+am\s+going\s+to|(?:we|i)\s+plan\s+to|planning\s+to|next,?\s+we)\b§)

(defparameter +retraction-opening+ #r§(?:(?:ok(?:ay)?|no|oh|actually|wait|hmm|sorry)\W+)*§)
(defparameter +retraction-phrase+ #r§[\p{L}\p{N}][\p{L}\p{N}'+#&/-]*(?:\s+[\p{L}\p{N}][\p{L}\p{N}'+#&/-]*)*?§)
(defparameter +retraction-close+ #r§\s*(?:[,;:.!]|$)§)
(defparameter +retraction-when+ #r§(?:\s+(?:from\s+)?(?:yesterday|earlier(?:\s+today)?|before|last\s+(?:week|night|time)|this\s+morning|the\s+other\s+day))?§)

(defparameter +bare-retraction+
  (compile-regex (cat #r§(?i)^§ +retraction-opening+ #r§(?:(?:scrap|scratch|forget|disregard|cancel)\s+(?:that|this|the\s+plan)\b|never\s*mind\b)§)))
(defparameter +named-retraction+
  (compile-regex (cat #r§(?i)^§ +retraction-opening+ #r§(?:scrap|scratch|forget|disregard|cancel)\s+(?:about\s+)?the\b§)))
(defparameter +phrase-retractions+
  (list
   (compile-regex (cat #r§(?i)^§ +retraction-opening+ #r§(?:let'?s\s+)?(?:scrap|scratch|drop|forget(?:\s+about)?|disregard|cancel|skip)\s+(?:the|my|our|that|this)\s+(§ +retraction-phrase+ #r§)\s+(?:idea|plan)s?(?:\s+after\s+all)?§ +retraction-when+ +retraction-close+))
   (compile-regex (cat #r§(?i)^§ +retraction-opening+ #r§no\s+(§ +retraction-phrase+ #r§)\s+after\s+all§ +retraction-when+ +retraction-close+))
   (compile-regex (cat #r§(?i)^§ +retraction-opening+ #r§(?:(?:i'?m|i\s+am|we'?re|we\s+are)\s+)?not\s+doing\s+(?:(?:the|my|our|that|this)\s+)?(§ +retraction-phrase+ #r§)(?:\s+(?:idea|plan)s?)?(?:\s+after\s+all|\s+any\s*more)?§ +retraction-when+ +retraction-close+))))

(defparameter +retraction-function-words+
  '("a" "an" "the" "my" "our" "your" "his" "her" "their" "its" "it" "this" "that" "these" "those" "whole"
    "entire" "also" "any" "some" "all" "about" "of" "for" "to" "in" "on" "at" "with" "and" "or" "idea" "plan"
    "ideas" "plans"))

(defparameter +retraction-idiom-words+
  '("problem" "problems" "prob" "probs" "worries" "worry" "need" "rush" "way" "matter" "doubt" "harm" "clue"
    "luck" "big" "deal" "biggie" "thanks" "wonder" "sweat" "well" "good" "great" "bad" "fine" "much" "anything"
    "nothing" "something" "everything" "so" "too" "either" "anymore" "today" "tonight" "tomorrow" "now"))

(defun retraction-words (phrase)
  (let ((words '()) (idiom t))
    (dolist (w (fields-func (to-lower phrase) (lambda (r) (or (space-p r) (char= r #\/)))))
      (let ((w (trim w "'-")))
        (when (and (string/= w "") (not (member-str w +retraction-function-words+)))
          (push-end w words)
          (setf idiom (and idiom (member-str w +retraction-idiom-words+) t)))))
    (if idiom "" (join words " "))))

(defun status-of-sentence (sentence)
  (let ((sentence (replace-all sentence "’" "'")))
    (cond ((question-p sentence) "")
          ((regex-match-p +hypothetical-wording+ sentence) +status-hypothetical+)
          ((regex-match-p +planned-wording+ sentence) +status-proposed+)
          (t ""))))

(defun retraction-of (sentence)
  "retractionOf: (values ok phrase words)."
  (let ((sentence (replace-all sentence "’" "'")))
    (when (question-p sentence) (return-from retraction-of (values nil "" "")))
    (dolist (pattern +phrase-retractions+)
      (let ((m (regex-find-submatch pattern sentence)))
        (when m
          (let ((words (retraction-words (second m))))
            (when (string/= words "")
              (return-from retraction-of (values t (second m) words)))))))
    (values (or (regex-match-p +bare-retraction+ sentence)
                (and (regex-match-p +named-retraction+ sentence) (has-specifics-p sentence)))
            "" "")))

(defun preference-p (sentence)
  (and (not (question-p sentence)) (some (lambda (p) (regex-match-p p sentence)) +preference-patterns+)))

(defun self-statement-p (sentence)
  (and (not (question-p sentence)) (some (lambda (p) (regex-match-p p sentence)) +self-statements+)))

(defun correction-of (sentence)
  "correctionOf: (values target ok)."
  (when (question-p sentence) (return-from correction-of (values (make-ctarget) nil)))
  (dolist (pattern (list +correction-not-but+ +correction-instead-x+ +correction-not-tail+))
    (dolist (m (regex-find-all-submatch pattern sentence))
      (let ((phrase (trim-space (second m))))
        (when (and (phrase-specifics phrase) (not (regex-match-p +additive+ phrase)))
          (return-from correction-of (values (make-ctarget :phrase phrase) t))))))
  (if (and (regex-match-p +correction-marker+ sentence) (has-specifics-p sentence))
      (values (make-ctarget) t)
      (values (make-ctarget) nil)))

(defun conversation-record (x sentence kind status day)
  (let ((o (make-obs :content sentence :kind kind :status status :subject (subject-in-text sentence (x-subjects x) (folded-aliases (x-ledger x))))))
    (when day
      (setf (obs-content o) (resolve-relative-dates sentence day))
      (setf (obs-event-date o) (event-date sentence day)))
    o))

(defun x-user (x i)
  (let* ((e (entry-at x i))
         (text (without-markup (text-blocks (jget (entry-message e) "content")))))
    (setf (x-exchange x) i (x-exchange-texts x) '())
    (when (string= text "") (return-from x-user))
    (push-end text (x-exchange-texts x))
    (let ((day (anchor-day e)))
      (dolist (sentence (sentences text))
        (unless (or (bare-ack-p sentence) (markup-only-p sentence) (gethash (normalized sentence) (x-known x)))
          (block next
            (multiple-value-bind (target correction) (correction-of sentence)
              (multiple-value-bind (retraction named-phrase named-words) (retraction-of sentence)
                (let ((kind "") (status "") (fallback nil))
                  (when (and correction (string= (ctarget-phrase target) "") retraction (string/= named-words ""))
                    (setf correction nil fallback target))
                  (setf retraction (and retraction (not correction)))
                  (cond (correction (setf kind "correction" status (status-of-sentence sentence)))
                        (retraction (setf kind "correction" status +status-retracted+))
                        ((preference-p sentence) (setf kind "preference"))
                        ((or (has-specifics-p sentence) (self-statement-p sentence))
                         (setf kind "fact" status (status-of-sentence sentence)))
                        (t (return-from next)))
                  (let ((r (x-add x (conversation-record x sentence kind status day) i)))
                    (when correction (setf (rec-correction r) target))
                    (setf (rec-retraction r) retraction)
                    (when retraction
                      (setf (rec-named-phrase r) named-phrase (rec-named-words r) named-words)
                      (when fallback
                        (setf (rec-fallback r) fallback (rec-fallback-status r) (status-of-sentence sentence))))))))))))))

(defun join-items (items) (join items " "))

(defun list-runs (items day)
  (let ((runs '()) (status "") (date ""))
    (loop for item in items for n from 0
          do (let ((item-status (status-of-sentence item))
                   (item-date (if day (event-date item day) "")))
               (if (or (= n 0) (string/= item-status status)
                       (and (string/= item-date "") (string/= date "") (string/= item-date date)))
                   (progn (push (list item) runs) (setf status item-status date item-date))
                   (progn (push-end item (car runs))
                          (when (string= date "") (setf date item-date))))))
    (nreverse runs)))

(defun x-reply (x i text)
  (when (string= (trim-space text) "") (return-from x-reply))
  (let* ((e (entry-at x i)) (day (anchor-day e)) (exchange (new-given (x-exchange-texts x))))
    (flet ((keep (statement)
             (unless (bare-ack-p statement)
               (let ((given (list exchange (x-known-given x))))
                 (dolist (r (x-records x))
                   (when (= (rec-exchange r) (x-exchange x))
                     (push-end (new-given (list (obs-content (rec-obs r)))) given)))
                 (when (added statement given)
                   (x-add x (conversation-record x statement "assistant" (status-of-sentence statement) day) i))))))
      (dolist (u (units text))
        (if (unit-items u)
            (dolist (run (list-runs (unit-items u) day)) (keep (join-items run)))
            (dolist (s (unit-sentences u)) (keep s)))))))

(defun conversational-p (kind)
  (member kind '("fact" "preference" "correction" "assistant" "event" "plan" "") :test #'string=))

(defun resolve-corrections (x resolve)
  (let ((earlier '()))
    (dolist (o (folded-active (x-ledger x)))
      (when (and (conversational-p (obs-kind o)) (not (gethash (obs-id o) (x-superseded x))))
        (push-end (list (obs-id o) (obs-content o) (obs-subject o) (obs-status o) (string= (obs-kind o) "assistant")) earlier)))
    (dolist (r (x-records x))
      (let ((o (rec-obs r)))
        (when (funcall resolve r)
          (let ((phrase (ctarget-phrase (rec-correction r))))
            (loop for c in (reverse earlier)
                  do (destructuring-bind (id content subject status assistant) c
                       (unless (or (gethash id (x-superseded x)) (not (may-supersede-p (obs-status o) status)))
                         (if (string/= phrase "")
                             (when (and (carries-phrase-p content phrase) (or (not assistant) (covered-by-phrase-p content phrase)))
                               (push-end id (obs-supersedes o))
                               (setf (gethash id (x-superseded x)) t))
                             (when (and (not assistant) (string/= (obs-subject o) "") (string= subject (obs-subject o))
                                        (restates-p (obs-content o) content))
                               (push-end id (obs-supersedes o))
                               (setf (gethash id (x-superseded x)) t)
                               (return))))))))
        (when (conversational-p (obs-kind o))
          (push-end (list (obs-id o) (obs-content o) (obs-subject o) (obs-status o) (string= (obs-kind o) "assistant")) earlier))))))

(defun previous-exchange (x exchange)
  (loop for i from (1- exchange) downto 0
        do (let ((e (entry-at x i)))
             (when (and (source-entry-p e) (string= (entry-type e) "message") (entry-message e)
                        (equal (jget (entry-message e) "role") "user"))
               (return-from previous-exchange i))))
  0)

(defun resolve-retractions (x)
  (let ((positions (entry-index-by-id (x-entries x))) (earlier '()) (kept '()))
    (flet ((pending-p (o) (and (conversational-p (obs-kind o))
                               (member (obs-status o) (list +status-proposed+ +status-hypothetical+) :test #'string=))))
      (dolist (o (folded-active (x-ledger x)))
        (when (and (pending-p o) (not (gethash (obs-id o) (x-superseded x))))
          (let ((anchor -1))
            (dolist (id (obs-source-ids o))
              (let ((at (gethash id positions))) (when at (setf anchor (max anchor at)))))
            (push-end (list (obs-id o) (obs-content o) (obs-status o) anchor) earlier))))
      (dolist (r (x-records x))
        (block next
          (let ((o (rec-obs r)))
            (when (rec-retraction r)
              (let ((withdrawn nil) (phrase (rec-named-phrase r)) (words (rec-named-words r)))
                (flet ((withdraw (id) (push-end id (obs-supersedes o)) (setf (gethash id (x-superseded x)) t withdrawn t)))
                  (cond
                    ((and (string/= words "") (phrase-specifics phrase))
                     (dolist (c earlier)
                       (when (and (not (gethash (first c) (x-superseded x))) (carries-phrase-p (second c) phrase))
                         (withdraw (first c)))))
                    ((string/= words "")
                     (dolist (c (reverse earlier))
                       (when (and (not (gethash (first c) (x-superseded x))) (may-supersede-p (obs-status o) (third c))
                                  (contains-words-p (second c) words))
                         (withdraw (first c))
                         (return))))
                    ((has-specifics-p (obs-content o))
                     (dolist (c earlier)
                       (when (and (not (gethash (first c) (x-superseded x))) (null (added-specifics (obs-content o) (list (second c)))))
                         (withdraw (first c)))))
                    (t
                     (let ((from (previous-exchange x (rec-exchange r))))
                       (dolist (c (reverse earlier))
                         (when (and (not (gethash (first c) (x-superseded x))) (>= (fourth c) from))
                           (withdraw (first c))
                           (return)))))))
                (unless withdrawn
                  (cond ((rec-fallback r)
                         (setf (obs-kind o) "correction" (obs-status o) (rec-fallback-status r) (rec-retraction r) nil
                               (rec-correction r) (rec-fallback r)))
                        ((not (has-specifics-p (obs-content o))) (return-from next))
                        (t (setf (obs-kind o) "fact" (obs-status o) "" (rec-retraction r) nil))))))
            (when (pending-p o)
              (push-end (list (obs-id o) (obs-content o) (obs-status o) (rec-anchor r)) earlier))
            (push r kept)))))
    (setf (x-records x) (nreverse kept))))

(defun link-records (x)
  "linkRecords: possibly_updates and conflicts between conversation records on one canonical
subject and scope."
  (let* ((active (folded-active (x-ledger x)))
         (subjects (subjects-of active)))
    (dolist (r (x-records x))
      (let ((subject (obs-subject (rec-obs r))))
        (when (and (string/= subject "") (not (member-str subject subjects)))
          (setf subjects (append subjects (list subject))))))
    (let ((canonical (canonical-subjects subjects (folded-aliases (x-ledger x)))) (earlier '()))
      (labels ((key (o)
                 (let ((subject (canonical-subject (canonical-of canonical (obs-subject o)))))
                   (if (string= subject "") "" (cat subject (string (code-char 0)) (normalize-scope (obs-scope o))))))
               (linkable (o)
                 (and (conversational-p (obs-kind o)) (string/= (obs-status o) +status-retracted+) (string/= (obs-subject o) ""))))
        (dolist (o active)
          (when (and (linkable o) (not (gethash (obs-id o) (x-superseded x))))
            (push-end o earlier)))
        (dolist (r (x-records x))
          (let ((o (rec-obs r)))
            (when (linkable o)
              (dolist (e earlier)
                (unless (or (gethash (obs-id e) (x-superseded x)) (string/= (key e) (key o))
                            (member-str (obs-id e) (obs-supersedes o)))
                  (when (and (may-supersede-p (obs-status o) (obs-status e)) (updates-specific-p (obs-content o) (obs-content e)))
                    (push-end (obs-id e) (obs-possibly-updates o)))
                  (when (negation-conflict-p (obs-content o) (obs-content e))
                    (push-end (obs-id e) (obs-conflicts o)))))
              (push-end o earlier))))))))

;;; work.go

;; fileTools, keyed by tool-key: one entry covers every harness's spelling of a name.
(defparameter +file-tools+
  '(("read" . "read") ("readfile" . "read") ("readfiles" . "read") ("view" . "read")
    ("write" . "written") ("writefile" . "written") ("createfile" . "written") ("create" . "written")
    ("edit" . "edited") ("editfile" . "edited") ("multiedit" . "edited") ("notebookedit" . "edited") ("replace" . "edited")
    ("delete" . "deleted") ("deletefile" . "deleted") ("remove" . "deleted") ("removefile" . "deleted")))
(defparameter +path-args+ '("path" "file_path" "filePath" "notebook_path"))
(defregex +exit-code+ #r§(?:Command|Process) exited with code (\d+)§)

(defun tool-key (name)
  "toolKey: ASCII lower case, without underscores or hyphens."
  (with-output-to-string (out)
    (loop for c across name
          do (cond ((find c "_-"))
                   ((char<= #\A c #\Z) (write-char (char-downcase c) out))
                   (t (write-char c out))))))

(defstruct tcall (id "") (name "") args raw)

(defun file-tool-state (call)
  "fileToolState: the state a successful call leaves its path in, nil when its tool names no file.
Kiro's write replacing a string (its command strReplace) edits the file."
  (let ((key (tool-key (tcall-name call))))
    (if (and (string= key "write") (equal (jget (tcall-args call) "command") "strReplace"))
        "edited"
        (cdr (assoc key +file-tools+ :test #'string=)))))

;;; patch.go

(defparameter +apply-patch-success+ "Success. Updated the following files:")

(defun apply-patch-word-p (w) (or (string= w "apply_patch") (string= w "applypatch")))

(defun path-base-name (p)
  "path.Base for a command word."
  (let* ((p (trim-right p "/")) (slash (position #\/ p :from-end t)))
    (cond ((string= p "") "/") (slash (subseq p (1+ slash))) (t p))))

(defun patch-of (call)
  "patchOf: (values patch t) for a call that hands Codex's apply_patch a patch, else (values \"\" nil)."
  (let ((args (tcall-args call)))
    (when (string= (tool-key (tcall-name call)) "applypatch")
      (let ((raw (tcall-raw call)))
        (return-from patch-of
          (values (cond ((stringp raw) raw) ((stringp (jget args "input")) (jget args "input")) (t "")) t))))
    (let ((script "") (command (jget args "command")))
      (cond ((stringp command) (setf script command))
            ((vectorp command)
             (unless (every #'stringp command) (return-from patch-of (values "" nil)))
             (let ((argv (coerce command 'list)))
               (cond ((and (= (length argv) 2) (apply-patch-word-p (first argv)))
                      (return-from patch-of (values (second argv) t)))
                     ((and (= (length argv) 3) (string= (path-base-name (first argv)) "bash")
                           (member-str (second argv) '("-lc" "-c")))
                      (setf script (third argv)))))))
      (when (string= script "")
        (let ((cmd (jget args "cmd"))) (when (stringp cmd) (setf script cmd))))
      (let ((words (fields (first (split script (string #\Newline))))))
        (if (and words (apply-patch-word-p (first words)) (contains script "*** Begin Patch"))
            (values script t)
            (values "" nil))))))

(defun patch-path-events (patch)
  "patchPathEvents: the (path state other) a patch touches, in its order."
  (let ((events (make-array 0 :adjustable t :fill-pointer t)) (in nil) (update -1))
    (dolist (raw (split patch (string #\Newline)))
      (let ((line (trim-right raw (coerce '(#\Space #\Tab #\Return) 'string))))
        (flet ((marker (prefix)
                 (when (has-prefix line prefix)
                   (let ((p (trim-space (subseq line (length prefix)))))
                     (and (string/= p "") p)))))
          (cond ((string= line "*** Begin Patch") (setf in t update -1))
                ((not in))
                (t (let ((moved update) p)
                     (setf update -1)
                     (cond ((string= line "*** End Patch") (setf in nil))
                           ((setf p (marker "*** Add File: ")) (vector-push-extend (list p "written" "") events))
                           ((setf p (marker "*** Delete File: ")) (vector-push-extend (list p "deleted" "") events))
                           ((setf p (marker "*** Update File: "))
                            (setf update (length events))
                            (vector-push-extend (list p "edited" "") events))
                           ((and (setf p (marker "*** Move to: ")) (>= moved 0))
                            (let ((from (first (aref events moved))))
                              (setf (aref events moved) (list from "moved to" p))
                              (vector-push-extend (list p "moved from" from) events))))))))))
    (coerce events 'list)))
(defstruct tresult (index 0) (text "") failed present details)
(defstruct fevent state other display sources)

(defun skipped-tool-p (x name)
  (and (string/= name "") (or (string= name +memory-tool+) (member-str name (x-skip-tools x)))))

(defun x-assistant (x i)
  (let* ((msg (entry-message (entry-at x i))) (content (jget msg "content")))
    (when (stringp content) (x-reply x i content) (return-from x-assistant))
    (let ((before '()) (after '()) (calls '()))
      (when (vectorp content)
        (loop for b across content
              do (when (hash-table-p b)
                   (let ((type (jget b "type")))
                     (cond ((equal type "text")
                            (let ((text (str (jget b "text"))))
                              (if (null calls) (push-end text before) (push-end text after))))
                           ((equal type "toolCall")
                            (push-end (make-tcall :id (str (jget b "id")) :name (str (jget b "name"))
                                                  :args (let ((a (jget b "arguments"))) (and (hash-table-p a) a))
                                                  :raw (jget b "arguments"))
                                      calls)))))))
      (let ((words (one-line (join before (string #\Newline))))
            (first (position-if-not (lambda (c) (skipped-tool-p x (tcall-name c))) calls)))
        (if (null first)
            (x-reply x i (join (append before after) (string #\Newline)))
            (x-reply x i (join after (string #\Newline))))
        (loop for call in calls for n from 0
              do (let ((result (result-of x i (tcall-id call))))
                   (cond ((string= (tcall-name call) +memory-tool+)
                          ;; work.go note, in src/legacy/notes.lisp (it reads the frozen query parser).
                          (x-memory-note x i call result))
                         ((skipped-tool-p x (tcall-name call)))
                         (t (x-call x i call result (if (eql n first) words ""))))))))))

(defun result-of (x call id)
  (when (string= id "") (return-from result-of (make-tresult)))
  (loop for i from (1+ call) below (length (x-entries x))
        do (let* ((e (entry-at x i)) (msg (entry-message e)))
             (when (and (string= (entry-type e) "message") msg)
               (let ((role (jget msg "role")))
                 (when (equal role "user") (return))
                 (when (and (equal role "toolResult") (equal (jget msg "toolCallId") id))
                   (let* ((text (text-blocks (jget msg "content")))
                          (failed (jtrue-p (jget msg "isError")))
                          (m (regex-find-submatch +exit-code+ text)))
                     (when (and m (string/= (second m) "0")) (setf failed t))
                     (return-from result-of
                       (make-tresult :index i :text text :failed failed :present t :details (jget msg "details")))))))))
  (make-tresult))

(defun x-bash-execution (x i)
  (let ((msg (entry-message (entry-at x i))))
    (when (jtrue-p (jget msg "excludeFromContext")) (return-from x-bash-execution))
    (let ((command (str (jget msg "command"))) (output (str (jget msg "output")))
          (failed (let ((code (jget msg "exitCode"))) (and (realp code) (/= code 0)))))
      (when (jtrue-p (jget msg "cancelled")) (setf failed t))
      (x-call x i (make-tcall :name "bash" :args (jobject "command" command))
              (make-tresult :index i :text output :failed failed :present t) ""))))

(defun path-key (x p)
  (let ((p (trim-space p)))
    (when (and (string/= (x-cwd x) "") (not (path-abs-p p)) (not (has-prefix p "~")))
      (setf p (path-join (x-cwd x) p)))
    (path-clean p)))

(defun path-display (x p)
  (let ((key (path-key x p)))
    (if (string/= (x-cwd x) "")
        (let ((rel (path-rel (path-clean (x-cwd x)) key)))
          (if (and rel (string/= rel "..") (not (has-prefix rel "../"))) rel key))
        key)))

(defun arg-string (args name) (let ((v (jget args name))) (and (stringp v) v)))

(defun shell-command (args)
  "shellCommand: the \"command\" argument, else \"cmd\" (Codex's exec_command)."
  (or (arg-string args "command") (arg-string args "cmd")))

(defun call-target (x call)
  "callTarget: (values key display), any secret in them redacted."
  (multiple-value-bind (key display) (raw-call-target x call)
    (values (redact-secrets key) (redact-secrets display))))

(defun raw-call-target (x call)
  "rawCallTarget: (values key display)."
  (let ((command (shell-command (tcall-args call))))
    (when (and command (string/= (trim-space command) ""))
      (return-from raw-call-target (values (trim-space command) (trim-space command)))))
  (dolist (arg +path-args+)
    (let ((p (arg-string (tcall-args call) arg)))
      (when (and p (string/= (trim-space p) ""))
        (return-from raw-call-target (values (path-key x p) (path-display x p))))))
  (multiple-value-bind (patch patch-p) (patch-of call)
    (when patch-p
      (let ((keys '()) (shown '()))
        (dolist (ev (patch-path-events patch))
          (unless (string= (second ev) "moved from")
            (push-end (path-key x (first ev)) keys)
            (push-end (path-display x (first ev)) shown)))
        (when keys (return-from raw-call-target (values (join keys " ") (join shown " ")))))))
  ;; JSONStringify of the call's arguments map: a nil map is boxed, so it encodes as null.
  (let ((args (if (tcall-args call) (json-string (tcall-args call) :escape-html nil) "null")))
    (values args args)))

(defun failure-line (text)
  (dolist (line (split text (string #\Newline)) "")
    (let ((line (trim-space line)))
      (unless (or (string= line "") (regex-match-p +exit-code+ line))
        (return line)))))

(defun attempt-line (tool display outcome result-text)
  (let ((line (cat tool " " (go-quote (clip display +brief-key-arg-runes+)) " " outcome)))
    (when (string= outcome "failed")
      (let ((reason (failure-line result-text)))
        (when (string/= reason "") (setf line (cat line ": " (clip reason +brief-key-arg-runes+))))))
    line))

(defun x-call (x i call result words)
  (let ((sources (if (and (tresult-present result) (/= (tresult-index result) i)) (list i (tresult-index result)) (list i))))
    (multiple-value-bind (key display) (call-target x call)
      (let* ((patch-p (nth-value 1 (patch-of call)))
             (patch (and patch-p (patch-of call)))
             (outcome (cond ((not (tresult-present result)) "open")
                            ((tresult-failed result) "failed")
                            ;; A patch applied only when apply_patch says so; any other result is its error.
                            ((and patch-p (not (contains (tresult-text result) +apply-patch-success+))) "failed")
                            (t "worked")))
             (akey (attempt-key (tcall-name call) key))
             (earlier (gethash akey (x-unresolved x))))
        (when (or (string/= outcome "worked") (string/= words "") earlier)
          (let ((line (attempt-line (tcall-name call) display outcome (tresult-text result)))
                (o (make-obs :kind "attempt" :outcome outcome :tool (tcall-name call) :target key))
                (said '()))
            (when (string= outcome "worked")
              (setf (obs-supersedes o) (append (obs-supersedes o) earlier))
              (dolist (id earlier)
                (let ((w (gethash id (x-words x) "")))
                  (when (and (string/= w "") (not (member-str w said))) (push-end w said)))))
            (when (and (string/= words "") (not (member-str words said))) (push-end words said))
            (setf (obs-content o) (if said (cat (join said " ") " [" line "]") line))
            (let ((r (apply #'x-add x o sources)))
              (setf (obs-id (rec-obs r)) (record-id (rec-obs r)))
              (setf (gethash (obs-id (rec-obs r)) (x-words x)) (join said " "))
              (if (string= outcome "worked")
                  (progn (dolist (id earlier) (setf (gethash id (x-superseded x)) t))
                         (remhash akey (x-unresolved x)))
                  (push-end (obs-id (rec-obs r)) (gethash akey (x-unresolved x)))))))
        (unless (string= outcome "worked") (return-from x-call))
        (when patch-p
          (dolist (ev (patch-path-events patch))
            (x-file-event x (first ev) (second ev) (third ev) sources)))
        (let ((state (file-tool-state call)))
          (when state
            (dolist (arg +path-args+)
              (let ((p (arg-string (tcall-args call) arg)))
                (when (and p (string/= (trim-space p) ""))
                  (x-file-event x p state "" sources)
                  (return))))))
        (let ((command (shell-command (tcall-args call))))
          (when command
            (dolist (ev (shell-path-events command))
              (x-file-event x (first ev) (second ev) (third ev) sources))
            (multiple-value-bind (hash subject ok) (commit-from command (tresult-text result))
              (when ok
                (apply #'x-add x (make-obs :content (cat "commit " hash " " subject) :kind "commit" :target hash) sources)))))))))

(defun x-file-event (x p state other sources)
  (let* ((key (path-key x p)) (previous (gethash key (x-file-events x))))
    (when (and (string= state "read")
               (or (gethash key (x-files x)) (and previous (string/= (fevent-state previous) "read"))))
      (return-from x-file-event))
    (unless previous (push-end key (x-file-order x)))
    (when (string/= other "") (setf other (path-display x other)))
    (setf (gethash key (x-file-events x)) (make-fevent :state state :other other :display (path-display x p) :sources sources))))

(defun mentions-p (message path)
  (and message (equal (jget message "role") "assistant") (vectorp (jget message "content"))
       (loop for b across (jget message "content")
             thereis (and (hash-table-p b) (equal (jget b "type") "toolCall")
                          (contains (json-stringify (jget b "arguments")) path)))))

(defun x-add-file-ops (x last)
  (let ((ops (stable-sort-list (x-file-ops x) #'string< :key #'first)))
    (dolist (op ops)
      (destructuring-bind (path state) op
        (let ((key (path-key x path)))
          (unless (or (gethash key (x-file-events x)) (gethash key (x-files x)) (string= (trim-space path) ""))
            (let ((source last))
              (loop for i from (max (min (x-file-ops-from x) (x-start x)) 0) to last
                    do (when (mentions-p (entry-message (entry-at x i)) path) (setf source i) (return)))
              (push-end key (x-file-order x))
              (setf (gethash key (x-file-events x))
                    (make-fevent :state state :other "" :display (path-display x path) :sources (list source))))))))))

(defun x-emit-files (x)
  (dolist (key (x-file-order x))
    (let* ((ev (gethash key (x-file-events x)))
           (content (cat (fevent-display ev) " " (fevent-state ev)
                         (if (string/= (fevent-other ev) "") (cat " " (fevent-other ev)) "")))
           (o (make-obs :content content :kind "file" :target key)))
      (dolist (id (gethash key (x-files x)))
        (unless (member-str id (obs-supersedes o)) (push-end id (obs-supersedes o))))
      (apply #'x-add x o (fevent-sources ev))))
  (setf (x-records x) (stable-sort (x-records x) #'< :key #'rec-anchor)))

;;; shell.go

(defregex +unsafe-shell+ #r§[;|<>`\n]|\$\(|(^|[^&])&([^&]|$)§)
(defregex +commit-line+ #r§(?m)^\[[^\]\s]+(?: \([^)]*\))? ([0-9a-f]{7,40})\] (.+)$§)
(defregex +is-git-commit+ #r§(?:^|&&\s*|\s)git\s+commit\b§)

(defun shell-words (part)
  "shellWords: (values words ok), each word (text . literal-p)."
  (let ((words '()) (b (make-string-output-stream)) (in-word nil) (literal t) (n (length part)) (i 0))
    (flet ((flush ()
             (let ((text (get-output-stream-string b)))
               (when in-word (push (cons text literal) words)))
             (setf in-word nil literal t)))
      (loop while (< i n)
            do (let ((c (char part i)))
                 (cond ((or (char= c #\Space) (char= c #\Tab)) (flush))
                       ((char= c #\')
                        (let ((end (position #\' part :start (1+ i))))
                          (unless end (return-from shell-words (values nil nil)))
                          (write-string part b :start (1+ i) :end end)
                          (setf in-word t i end)))
                       ((char= c #\")
                        (let ((end (position #\" part :start (1+ i))))
                          (unless end (return-from shell-words (values nil nil)))
                          (let ((quoted (subseq part (1+ i) end)))
                            (when (contains-any quoted "$\\") (setf literal nil))
                            (write-string quoted b))
                          (setf in-word t i end)))
                       ((char= c #\\)
                        (when (< (1+ i) n) (write-char (char part (1+ i)) b) (incf i))
                        (setf in-word t))
                       (t (when (find c "*?[]{}~$") (setf literal nil))
                          (write-char c b)
                          (setf in-word t))))
               (incf i))
      (flush))
    (values (nreverse words) t)))

(defun simple-commands (command)
  (let ((command (trim-space command)) (commands '()))
    (when (or (string= command "") (regex-match-p +unsafe-shell+ command))
      (return-from simple-commands (values nil nil)))
    (dolist (part (split command "&&"))
      (multiple-value-bind (words ok) (shell-words part)
        (when (or (not ok) (null words)) (return-from simple-commands (values nil nil)))
        (when (member (car (first words)) '("cd" "pushd" "popd") :test #'string=)
          (return-from simple-commands (values nil nil)))
        (when (find #\= (car (first words))) (return-from simple-commands (values nil nil)))
        (when (string= (car (first words)) "git")
          (dolist (w (rest words))
            (when (or (string= (car w) "-C") (has-prefix (car w) "--git-dir") (has-prefix (car w) "--work-tree"))
              (return-from simple-commands (values nil nil)))))
        (push-end words commands)))
    (values commands t)))

(defun operands (words)
  (let ((out '()) (options '()) (end-options nil))
    (dolist (w words)
      (cond ((and (not end-options) (string= (car w) "--")) (setf end-options t))
            ((and (not end-options) (has-prefix (car w) "-") (string/= (car w) "-")) (push-end (car w) options))
            (t (push-end w out))))
    (values out options)))

(defun has-option-p (options &rest names)
  (some (lambda (option) (some (lambda (name) (or (string= option name) (has-prefix option (cat name "=")))) names)) options))

(defun interactive-rm-p (options)
  (some (lambda (option)
          (or (string= option "--interactive")
              (and (has-prefix option "--interactive=") (string/= option "--interactive=never"))
              (and (not (has-prefix option "--")) (find-if (lambda (c) (find c "iI")) (subseq option 1)))))
        options))

(defun patch-mode-options-p (options)
  (some (lambda (option)
          (or (string= option "--patch")
              (and (not (has-prefix option "--")) (has-prefix option "-") (find #\p (subseq option 1)))))
        options))

(defun options-prefix (words)
  (loop for w in words for i from 0
        do (when (or (string= (car w) "--") (not (has-prefix (car w) "-")))
             (return-from options-prefix (subseq words 0 i))))
  words)

(defun shell-path-events (command)
  "shellPathEvents: (path state other) lists."
  (multiple-value-bind (commands ok) (simple-commands command)
    (unless ok (return-from shell-path-events nil))
    (let ((events '()))
      (flet ((literal-paths (words state)
               (dolist (w words)
                 (when (and (cdr w) (string/= (car w) "")) (push-end (list (car w) state "") events)))))
        (dolist (words commands)
          (cond
            ((string= (car (first words)) "rm")
             (multiple-value-bind (paths options) (operands (rest words))
               (unless (interactive-rm-p options) (literal-paths paths "deleted"))))
            ((and (string= (car (first words)) "git") (> (length words) 1))
             (let ((sub (car (second words))) (rest (cddr words)))
               (cond
                 ((string= sub "rm")
                  (multiple-value-bind (paths options) (operands rest)
                    (unless (has-option-p options "--cached" "-n" "--dry-run") (literal-paths paths "deleted"))))
                 ((string= sub "mv")
                  (multiple-value-bind (paths options) (operands rest)
                    (when (and (= (length paths) 2) (cdr (first paths)) (cdr (second paths))
                               (not (has-option-p options "-n" "--dry-run")))
                      (push-end (list (car (first paths)) "moved to" (car (second paths))) events)
                      (push-end (list (car (second paths)) "moved from" (car (first paths))) events))))
                 ((string= sub "checkout")
                  (let ((dash (position "--" rest :key #'car :test #'string=)))
                    (when (and dash (= dash (length (options-prefix rest)))
                               (not (patch-mode-options-p (mapcar #'car (options-prefix rest)))))
                      (literal-paths (nthcdr (1+ dash) rest) "reverted"))))
                 ((string= sub "restore")
                  (multiple-value-bind (paths options) (operands rest)
                    (let ((staged (has-option-p options "--staged" "-S"))
                          (worktree (has-option-p options "--worktree" "-W")))
                      (when (and (or (not staged) worktree) (not (has-option-p options "--source" "-s"))
                                 (not (patch-mode-options-p options)))
                        (literal-paths paths "reverted")))))))))))
      events)))

(defun commit-from (command result)
  (if (not (regex-match-p +is-git-commit+ command))
      (values "" "" nil)
      (let ((m (regex-find-submatch +commit-line+ result)))
        (if m (values (second m) (trim-space (third m)) t) (values "" "" nil)))))

;;; extract.go

(defun mergeable-p (a b)
  (let ((ao (rec-obs a)) (bo (rec-obs b)))
    (and (member (obs-kind ao) '("fact" "preference" "assistant") :test #'string=)
         (string= (obs-kind ao) (obs-kind bo)) (string= (obs-status ao) (obs-status bo))
         (string/= (obs-subject ao) "") (string= (obs-subject ao) (obs-subject bo))
         (= (rec-exchange a) (rec-exchange b)) (null (rec-correction a)) (null (rec-correction b)))))

(defun x-merge (x)
  (let ((merged '()))
    (dolist (r (x-records x))
      (if (and merged (mergeable-p (first merged) r))
          (let* ((prev (first merged)) (po (rec-obs prev)) (ro (rec-obs r))
                 (prev-undated (or (rec-undated prev) (names-no-one-date-p po)))
                 (part-undated (or (rec-undated r) (names-no-one-date-p ro))))
            (setf (obs-content po) (truncate-record-content (redact-secrets (cat (obs-content po) " " (obs-content ro)))))
            (dolist (id (obs-source-ids ro))
              (unless (member-str id (obs-source-ids po)) (push-end id (obs-source-ids po))))
            (setf (rec-anchor prev) (max (rec-anchor prev) (rec-anchor r)))
            (cond ((or prev-undated part-undated) (setf (obs-event-date po) "" (rec-undated prev) t))
                  ((or (string= (obs-event-date ro) "") (string= (obs-event-date ro) (obs-event-date po))))
                  ((string= (obs-event-date po) "") (setf (obs-event-date po) (obs-event-date ro)))
                  (t (setf (obs-event-date po) "" (rec-undated prev) t))))
          (push r merged)))
    (setf (x-records x) (nreverse merged))))

(defun x-assign-ids (x)
  (let ((kept '()) (seen (make-set)))
    (dolist (r (x-records x))
      (let ((o (rec-obs r)))
        (when (obs-source-ids o)
          (when (string= (obs-id o) "") (setf (obs-id o) (record-id o)))
          (unless (or (gethash (obs-id o) (folded-by-id (x-ledger x))) (gethash (obs-id o) seen))
            (setf (gethash (obs-id o) seen) t)
            (setf (obs-token-count o) (observation-line-tokens (obs-id o) (obs-timestamp o) (obs-relevance o) (obs-content o)))
            (push r kept)))))
    (setf (x-records x) (nreverse kept))))

(defun segment-end (entries anchor last)
  (let ((end anchor))
    (loop for i from (1+ anchor) to last
          while (< i (length entries))
          do (let ((e (aref entries i)))
               (when (source-entry-p e)
                 (if (and (string= (entry-type e) "message") (entry-message e)
                          (equal (jget (entry-message e) "role") "toolResult"))
                     (setf end i)
                     (return)))))
    end))

(defun x-batches (x last)
  (let ((out '()))
    (dolist (r (x-records x))
      (let ((cover (entry-id (entry-at x (segment-end (x-entries x) (rec-anchor r) last)))))
        (if (and out (string= (batch-covers (first out)) cover))
            (push-end (rec-obs r) (batch-observations (first out)))
            (push (make-batch :observations (list (rec-obs r)) :covers cover) out))))
    (nreverse out)))

(defun extract (entries start end ledger &key (skip-tools '()) (cwd "") (file-ops '()) (file-ops-from 0))
  "Extract: (values batches covers-up-to-id). entries is a vector."
  (let* ((start (max start 0)) (end (min end (length entries)))
         (x (make-xstate :entries entries :start start :end end :skip-tools skip-tools :ledger ledger :cwd cwd
                         :file-ops file-ops :file-ops-from file-ops-from))
         (last -1))
    (load-ledger x)
    (loop for i from start below end
          do (let ((e (aref entries i)))
               (when (source-entry-p e)
                 (setf last i)
                 (when (and (string= (entry-type e) "message") (entry-message e))
                   (let ((role (jget (entry-message e) "role")))
                     (cond ((equal role "user") (x-user x i))
                           ((equal role "assistant") (x-assistant x i))
                           ((equal role "bashExecution") (x-bash-execution x i))))))))
    (let ((cite last))
      (when (and (< cite 0) file-ops)
        (loop for i from (1- end) downto (max file-ops-from 0)
              do (when (source-entry-p (aref entries i)) (setf cite i) (return))))
      (when (< cite 0) (return-from extract (values '() "")))
      (x-add-file-ops x cite)
      (x-emit-files x)
      (x-merge x)
      (x-assign-ids x)
      (resolve-corrections x (lambda (r) (rec-correction r)))
      (resolve-retractions x)
      (resolve-corrections x (lambda (r) (and (rec-correction r) (eq (rec-correction r) (rec-fallback r)))))
      (link-records x)
      (values (x-batches x cite) (if (>= last 0) (entry-id (aref entries last)) "")))))

;;; drop.go

(defun drop-closure (ids active by-id)
  "DropClosure: ids with every record they supersede, transitively."
  (let ((lookup (make-set)) (out (make-set)) (queue (copy-list ids)))
    (dolist (o active) (setf (gethash (obs-id o) lookup) o))
    (loop while queue
          do (let ((id (pop queue)))
               (unless (gethash id out)
                 (setf (gethash id out) t)
                 (let ((o (or (gethash id lookup) (and by-id (gethash id by-id)))))
                   (when o
                     (dolist (next (obs-supersedes o))
                       (when (and (string/= next id) (not (gethash next out)))
                         (setf queue (append queue (list next))))))))))
    out))

(defun drops (active rendered by-id &optional rendered-reflections)
  "Drops: the active records a rendered record supersedes or a rendered reflection cites,
transitively, that are not rendered themselves."
  (let ((direct '()) (shown (make-set)))
    (dolist (o rendered)
      (dolist (id (obs-supersedes o)) (unless (string= id (obs-id o)) (push-end id direct))))
    (dolist (r rendered-reflections)
      (setf direct (append direct (refl-supporting r))))
    (dolist (o rendered) (setf (gethash (obs-id o) shown) t))
    (let ((replaced (drop-closure direct active by-id)))
      (loop for o in active
            when (and (gethash (obs-id o) replaced) (not (gethash (obs-id o) shown)))
              collect (obs-id o)))))
