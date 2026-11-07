;;;; Level 0's conversation encoding beyond preferences (memory/extract/encoding.go and
;;;; convention.go, memory/ledger/keys.go): standing rules and scope exclusions from the user's
;;;; turns, decisions in fixed wording, the keys a rule or a decision is about, and the
;;;; repository conventions a read tool's result shows. Byte for byte as Go
;;;; (contracts/conversation.jsonl, lisp/tests/conversation.lisp).

(in-package "OM.ENGINE")

;;; keys.go

(defregex +key-span+ #r§`([^`
]+)`§)
(defregex +key-path+ #r§(?:^|[\s(\["'])((?:~|\.{1,2})?/?[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]*)+)§)

(defparameter +key-path-words+ '("and/or" "either/or"))

(defun record-keys (text)
  "RecordKeys: the code spans' text, then the bare paths outside them, in order, once each."
  (let ((out '()))
    (flet ((add (k) (when (and (string/= k "") (not (member-str k out))) (push-end k out))))
      (dolist (m (regex-find-all-submatch +key-span+ text))
        (add (trim-space (second m))))
      (let ((bare (regex-replace-all +key-span+ text " ")))
        (dolist (m (regex-find-all-submatch +key-path+ bare))
          (let ((k (trim-right (second m) ".,;:")))
            (when (and (>= (length k) 4) (contains-any (to-lower k) "abcdefghijklmnopqrstuvwxyz")
                       (not (member-str (to-lower k) +key-path-words+)))
              (add k))))))
    out))

(defun missing-keys (text replacement)
  "MissingKeys: text's keys replacement does not hold byte for byte."
  (remove-if (lambda (k) (contains replacement k)) (record-keys text)))

(defun keeps-keys-p (o)
  "KeepsKeys: a user rule or correction, or a decision."
  (or (user-priority-p o) (string= (obs-kind o) "decision")))

;;; encoding.go

(defparameter +rule-patterns+
  (mapcar #'compile-regex
          (list #r§(?i)\balways\s+(?:touch|edit|modify|change|import|install|bump|upgrade|delete|rename|commit|push|deploy|run)\b§
                #r§(?i)\bnever\s+(?:touch|edit|modify|change|import|install|bump|upgrade|delete|rename|commit|push|deploy|run)\b§
                #r§(?i)\buse\s+\S.*?\s+instead\s+of\s+\S§
                #r§(?i)\buse\s+[^,.;:!?]+,\s*not\s+\S§
                #r§(?i)\bprefer\s+\S.*?\s+over\s+\S§
                #r§(?i)\bfrom\s+now\s+on\b§
                #r§(?i)\b(?:always|never)\b.*\bfrom\s+now\b§
                #r§(?i)\bbefore\s+(?:you\s+|we\s+|i\s+)?(?:commit|push|merg)\w*§
                #r§(?i)\bafter\s+(?:each|every)\s+\w+.*?\brun\b§
                #r§(?i)\bmake\s+sure\s+(?:to|you|that)\b.*\bbefore\b§
                #r§(?i)\bwe\s+(?:always|never|use|prefer)\b§
                #r§(?i)\bour\s+convention\s+is\b§)))

(defparameter +scope-patterns+
  (mapcar #'compile-regex
          (list #r§(?i)\b(?:don'?t|do\s+not|never)\s+(?:touch|edit|modify|change)\s+(\S+)§
                #r§(?i)\bleave\s+(\S+)[^.;!?]*?\s+alone\b§
                #r§(?i)\bonly\s+(?:modify|change|edit|touch)\s+(\S+)§
                #r§(?i)\bstay\s+out\s+of\s+(\S+)§)))

(defregex +rule-hypothetical+ #r§(?i)\b(?:if\s+(?:we|i)|suppose|supposing|what\s+if|hypothetically)\b§)
(defregex +rule-one-time+ #r§(?i)\b(?:(?:just\s+)?this\s+once|just\s+once|(?:only\s+)?this\s+(?:one\s+)?time|one[-\s](?:time|off)|for\s+this\s+(?:task|change|commit|pr|one|run|session|ticket|request)|(?:it|that)\s+once)\b§)

(defparameter +decision-patterns+
  (mapcar #'compile-regex
          (list #r§(?i)\blet'?s\s+go\s+with\b§
                #r§(?i)\b(?:we'?re|we\s+are|i'?m|i\s+am)\s+going\s+with\b§
                #r§(?i)\b(?:we'?ll|we\s+will|i'?ll|i\s+will)\s+(?:use|go\s+with|stick\s+with)\b.*\b(?:because|since)\b§
                #r§(?i)\bdecided\s+(?:to|on|against)\b§
                #r§(?i)\b(?:chose|opted\s+for|settled\s+on)\b§
                #r§(?i)\brejected\b.*\bbecause\b§)))

(defregex +decision-unmade+ #r§(?i)(?:\bnot|n't|\bnever)\s+(?:yet\s+)?decided\b§)
(defregex +decision-reason+ #r§(?i)\b(?:because|since|so\s+that)\b§)

(defparameter +scope-pronouns+
  '("it" "that" "this" "them" "those" "these" "me" "us" "him" "her" "anything" "everything" "something" "nothing"))

(defun one-time-p (text)
  "extract.IsOneTime: a permission or instruction for one occasion (decision 23), which the core
never takes as a standing rule."
  (regex-match-p +rule-one-time+ (replace-all text "’" "'")))

(defun rule-p (sentence)
  "isRule: a standing rule, scope exclusion or policy form, neither a question, posed
hypothetically, reported from someone else nor a one-time permission."
  (let ((sentence (replace-all sentence "’" "'")))
    (when (or (question-p sentence) (regex-match-p +rule-hypothetical+ sentence)
              (reported-p sentence) (regex-match-p +rule-one-time+ sentence))
      (return-from rule-p nil))
    (when (some (lambda (p) (regex-match-p p sentence)) +rule-patterns+)
      (return-from rule-p t))
    (let ((keyed (and (record-keys sentence) t)))
      (dolist (p +scope-patterns+)
        (let ((m (regex-find-submatch p sentence)))
          (when m
            (let ((object (to-lower (trim (second m) "`'\".,;:!?()"))))
              (when (or keyed (and (string/= object "") (not (member-str object +scope-pronouns+))))
                (return-from rule-p t))))))
      (policy-rule-p sentence keyed))))

;;; The policy forms of a standing rule (rules.go, isPolicyRule).

(defregex +policy-never-always+ #r§(?i)(?:^(?:(?:ok(?:ay)?|so|also|and|but|oh|please|remember|important|note|reminder)\W+)*|[:;,(]\s*|\b(?:and|or|but|please|should|to)\s+)(?:never|always)\s+([a-z][a-z-]*)\b([^.;:!?]*)§)
(defregex +policy-modal+ #r§(?i)\b(?:must|shall)(?:\s+not|n't|nt)?\s+(?:(?:always|never|only|not|also|first)\s+)*([a-z][a-z'-]*)\b([^.;:!?]*)§)
(defregex +policy-modal-epistemic+ #r§(?i)\b(?:i|it|that|this|there|which|what|who)\s+(?:must|shall)\b|\bmust'?ve\b|\bmust\s+have\s+(?:been|done|made|got|gone|seen|run|left|lost|forgot|forgotten|broken|written|[a-z]+ed)\b|\bmust\s+be\s+(?:the|a|an|my|your|our|some|something|because|wrong|right|why|it|that|so|joking|kidding|tired|confused|nice|flaky|busy|down|slow|off|missing|stale|weird|hard|easy|great|crazy)\b§)
(defregex +policy-modal-guess+ #r§(?i)\b(?:they|he|she|you|we)\s+must\s+be\s+([a-z]+)§)
(defregex +policy-allowed+ #r§(?i)(?:\b(?:is|are)(?:n't|nt)?|'s|'re)\s+(?:(?:not|never|strictly|only|also|absolutely)\s+)*(?:allowed|permitted|prohibited|forbidden|disallowed|banned)\b§)
(defregex +policy-approval+ #r§(?i)\b(requires?|required|needs?|needed)\b([^.;:!?]*?)\b(approval|review|sign-?off|signoff)s?\b§)
(defregex +policy-approval-own+ #r§(?i)\b(?:i|we)\s+(?:really\s+|still\s+|just\s+)?(?:need|needs|needed|require|required)\b§)

(defparameter +policy-markers+
  (mapcar #'compile-regex
          (list #r§(?i)(?:^|\b(?:company|team|org|organi[sz]ation|security|house|project|repo|our)\s+)(?:policy|rule)\s*:§
                #r§(?i)\b(?:company|team|org|organi[sz]ation|security|house|our)\s+(?:policy|rule)\s+(?:is|says|states|requires)\b§
                #r§(?i)\b(?:we\s+have|there\s+is|there's|i\s+have|our\s+team\s+has)\s+a\s+(?:[a-z-]+\s+)?(?:rule|policy)\s+(?:that|to|against)\b§
                #r§(?i)\b(?:the|our|my)\s+rule\s+is\s+(?:that|to)\b§)))

(defregex +policy-only+ #r§(?i)(?:^|[:;,]\s*|\b(?:and|please|you|we|to|should|must|may|can)\s+)only\s+([a-z][a-z-]*)\b([^.;:!?]*)§)
(defregex +policy-key-only+ #r§(?i)(`[^`\n]+`|[A-Za-z0-9_./~-]+)\s+only\b(?:\s+([a-z]+))?§)
(defregex +policy-key-only-subject+ #r§(?i)^(?:i|it|that|this|he|she|they|there)\b§)
(defregex +policy-quoted+ #r§"[^"\n]*"|“[^”\n]*”§)
(defregex +policy-token+ #r§[A-Za-z0-9][A-Za-z0-9_'./@$-]*§)
(defregex +rule-reported+ #r§(?i)\b(?:he|she|they|someone|somebody|people|everyone|everybody|(?:my|our|his|her|their|the|a)\s+(?:boss|manager|lead|colleague|coworker|co-worker|teammate|friend|client|customer|reviewer|cto|ceo|admin|mentor|teacher))\s+(?:said|says|told|tells|wrote|writes|mentioned|claims|claimed|insists|insisted|thinks|thought|suggested|suggests)\b|\baccording\s+to\b|\b(?:i\s+was|we\s+were|i've\s+been|we've\s+been)\s+told\b§)
(defregex +rule-reported-name+ #r§\b([A-Z][a-z]+)\s+(?:said|says|told|wrote|mentioned|claims|claimed|thinks|suggested)\b§)

(defparameter +rule-reported-name-words+ '("Policy" "Rule" "Docs" "Documentation" "Guidelines" "Spec" "Readme"))

(defparameter +policy-verb-stop+
  '("mind" "again" "ever" "before" "been" "had" "has" "was" "were" "is" "are" "did" "does" "got"
    "the" "a" "an" "so" "that" "this" "it" "really" "quite" "sure" "enough" "once" "too"
    "more" "less" "heard" "seen" "said" "thought" "knew" "told" "went" "came" "used" "true"
    "false" "if" "when" "after" "for" "in" "on" "one" "two" "just" "here" "there" "then" "now"
    "very" "much" "like" "until" "because" "with" "to" "of" "at" "by" "as" "some" "all"
    "what" "how" "why" "who" "know" "think" "guess" "seem" "seems" "happen" "happens" "work" "works"))

(defparameter +policy-ing-verbs+ '("bring" "string" "ping" "sing" "ring" "swing" "sting" "fling" "cling" "wring"))

(defparameter +policy-fillers+
  (let ((table (make-hash-table :test #'equal)))
    (dolist (w (fields "i me my mine you your yours we us our ours they them their theirs he him his she her hers it its myself yourself ourselves themselves
		this that these those anything everything something nothing anyone anybody someone somebody everyone everybody nobody one ones
		a an the any some all every each no none such other another
		to of in on at for with without from by into onto about over under up down out off away back through after before as than like via per
		and or but nor so if when unless until while because then also
		again ever here there now just really very too even still yet already anymore today first more less much many well please
		be is are was were been being am do does did done have has had can could will would should shall may might must not never always only
		get got go going let lets make sure ok okay fine good right wrong true false ready careful thing things stuff way
		policy rule company team org approval review signoff sign-off allowed permitted prohibited forbidden disallowed banned
		require requires required need needs needed")
             table)
      (setf (gethash w table) t))))

(defparameter +policy-key-only-next+
  '("is" "are" "was" "were" "has" "have" "had" "does" "did" "contains" "returns" "takes" "gets"
    "seems" "appears" "runs" "happens" "exists" "fails" "breaks" "works" "matters" "shows"))

(defun policy-content-p (text)
  "policyContent: a word in text that is not a filler."
  (dolist (tok (regex-find-all-strings +policy-token+ text) nil)
    (let ((w (to-lower (trim-right tok "'.-"))))
      (when (and (string/= w "") (not (gethash w +policy-fillers+)))
        (return t)))))

(defun policy-verb-p (word)
  "policyVerb: a word an imperative may open with."
  (let ((w (to-lower word)))
    (cond ((member-str w +policy-verb-stop+) nil)
          ((and (has-suffix w "ed") (not (has-suffix w "eed")) (> (length w) 4)) nil)
          ((and (has-suffix w "s") (not (has-suffix w "ss")) (not (has-suffix w "us"))) nil)
          ((and (has-suffix w "ing") (not (member-str w +policy-ing-verbs+))) nil)
          (t t))))

(defun policy-occasion-p (tail)
  "policyOccasion: an object that is the thing at hand (\"it\", \"them\", a bare \"this\" or \"that\")."
  (let ((tokens (regex-find-all-strings +policy-token+ tail)))
    (when tokens
      (let ((first (to-lower (first tokens))))
        (cond ((member-str first '("it" "them")) t)
              ((member-str first '("this" "that"))
               (or (null (rest tokens))
                   (and (gethash (to-lower (trim-right (second tokens) "'.-")) +policy-fillers+) t)))
              (t nil))))))

(defun modal-guess-p (text)
  "isModalGuess: a pronoun's \"must be X\" with X no participle."
  (let ((m (regex-find-submatch +policy-modal-guess+ text)))
    (and m (let ((w (to-lower (second m)))) (and (not (has-suffix w "ed")) (not (has-suffix w "en")))))))

(defun reported-p (sentence)
  "isReported: a sentence stating someone else's words."
  (or (regex-match-p +rule-reported+ sentence)
      (some (lambda (m) (not (member-str (second m) +rule-reported-name-words+)))
            (regex-find-all-submatch +rule-reported-name+ sentence))))

(defun policy-rule-p (sentence keyed)
  "isPolicyRule: a policy form of a standing rule, read outside double-quoted spans."
  (let ((text (regex-replace-all +policy-quoted+ sentence " ")))
    (dolist (m (regex-find-all-submatch +policy-never-always+ text))
      (when (and (policy-verb-p (second m)) (not (policy-occasion-p (third m))) (or keyed (policy-content-p (third m))))
        (return-from policy-rule-p t)))
    (unless (or (regex-match-p +policy-modal-epistemic+ text) (modal-guess-p text))
      (let ((loc (regex-find-submatch-index +policy-modal+ text)))
        (when loc
          (let ((verb (subseq text (aref loc 2) (aref loc 3))))
            (when (and (or (string-equal verb "be") (policy-verb-p verb))
                       (not (policy-occasion-p (subseq text (aref loc 4) (aref loc 5))))
                       (or keyed (policy-content-p (cat (subseq text 0 (aref loc 0)) " " (subseq text (aref loc 4) (aref loc 5))))))
              (return-from policy-rule-p t))))))
    (let ((loc (regex-find-submatch-index +policy-allowed+ text)))
      (when (and loc (or keyed (policy-content-p (cat (subseq text 0 (aref loc 0)) " " (subseq text (aref loc 1))))))
        (return-from policy-rule-p t)))
    (unless (regex-match-p +policy-approval-own+ text)
      (let ((loc (regex-find-submatch-index +policy-approval+ text)))
        (when loc
          (let ((verb (to-lower (subseq text (aref loc 2) (aref loc 3))))
                (noun (to-lower (subseq text (aref loc 6) (aref loc 7)))))
            (when (and (or (not (has-prefix verb "need")) (string= noun "approval"))
                       (or keyed (policy-content-p (cat (subseq text 0 (aref loc 0)) " " (subseq text (aref loc 4) (aref loc 5))
                                                        " " (subseq text (aref loc 1))))))
              (return-from policy-rule-p t))))))
    (dolist (p +policy-markers+)
      (let ((loc (regex-find-submatch-index p text)))
        (when (and loc (or keyed (policy-content-p (subseq text (aref loc 1)))))
          (return-from policy-rule-p t))))
    (unless keyed (return-from policy-rule-p nil))
    (dolist (m (regex-find-all-submatch +policy-only+ text))
      (when (and (policy-verb-p (second m)) (record-keys (third m)))
        (return-from policy-rule-p t)))
    (when (regex-match-p +policy-key-only-subject+ text) (return-from policy-rule-p nil))
    (let ((keys (record-keys sentence)))
      (dolist (m (regex-find-all-submatch +policy-key-only+ text) nil)
        (when (and (member-str (trim (second m) "`") keys)
                   (not (member-str (to-lower (third m)) +policy-key-only-next+)))
          (return-from policy-rule-p t))))))

(defun decision-p (sentence)
  "isDecision: decision wording, not a question, a condition or a decision not taken."
  (let ((sentence (replace-all sentence "’" "'")))
    (and (not (question-p sentence))
         (not (regex-match-p +hypothetical-wording+ sentence))
         (not (regex-match-p +decision-unmade+ sentence))
         (some (lambda (p) (regex-match-p p sentence)) +decision-patterns+)
         t)))

(defun assistant-decision-p (statement)
  "isAssistantDecision: a decision stated with its reason."
  (and (decision-p statement) (regex-match-p +decision-reason+ statement) t))

(defregex +key-file+ #r§^[A-Za-z0-9_.-]*[A-Za-z0-9_-]\.[A-Za-z][A-Za-z0-9]{0,7}$§)

(defun user-rule (o)
  "userRule: a user rule's or decision's source and authority, and its keys as paths or commands."
  (setf (obs-source o) "user-said" (obs-authority o) "user")
  (dolist (k (record-keys (obs-content o)) o)
    (setf (obs-keys o) (keys-add (obs-keys o) (if (or (contains k "/") (regex-match-p +key-file+ k)) "paths" "commands") k))))

(defun exchange-has-decision-p (x)
  "exchangeHasDecision."
  (some (lambda (r) (and (= (rec-exchange r) (x-exchange x)) (string= (obs-kind (rec-obs r)) "decision")))
        (x-records x)))

;;; convention.go

(defparameter +convention-managers+
  '(("package-lock.json" . "npm") ("npm-shrinkwrap.json" . "npm") ("yarn.lock" . "yarn") ("pnpm-lock.yaml" . "pnpm")
    ("bun.lock" . "bun") ("bun.lockb" . "bun") ("Cargo.lock" . "cargo") ("go.sum" . "go modules") ("poetry.lock" . "poetry")
    ("uv.lock" . "uv") ("Pipfile.lock" . "pipenv") ("Gemfile.lock" . "bundler") ("composer.lock" . "composer")))

(defparameter +convention-tools+
  '((".prettierrc" . "formatter prettier") (".prettierrc.json" . "formatter prettier") (".prettierrc.yaml" . "formatter prettier")
    (".prettierrc.yml" . "formatter prettier") (".prettierrc.js" . "formatter prettier") (".prettierrc.cjs" . "formatter prettier")
    (".prettierrc.mjs" . "formatter prettier") (".prettierrc.toml" . "formatter prettier") ("prettier.config.js" . "formatter prettier")
    ("prettier.config.cjs" . "formatter prettier") ("prettier.config.mjs" . "formatter prettier")
    (".eslintrc" . "linter eslint") (".eslintrc.json" . "linter eslint") (".eslintrc.js" . "linter eslint") (".eslintrc.cjs" . "linter eslint")
    (".eslintrc.yaml" . "linter eslint") (".eslintrc.yml" . "linter eslint") ("eslint.config.js" . "linter eslint")
    ("eslint.config.mjs" . "linter eslint") ("eslint.config.cjs" . "linter eslint") ("eslint.config.ts" . "linter eslint")
    ("ruff.toml" . "linter ruff") (".ruff.toml" . "linter ruff") ("rustfmt.toml" . "formatter rustfmt") (".rustfmt.toml" . "formatter rustfmt")
    (".golangci.yml" . "linter golangci-lint") (".golangci.yaml" . "linter golangci-lint") (".golangci.toml" . "linter golangci-lint")
    ("go.mod" . "formatter gofmt (implied by go.mod)")))

(defregex +convention-line-number+ #r§^\s*\d+(?:→|	)§)
(defregex +editorconfig-setting+ #r§^\s*(indent_style|indent_size|tab_width|end_of_line|charset|max_line_length|insert_final_newline|trim_trailing_whitespace)\s*=\s*(\S+)§)
(defregex +pyproject-tool+ #r§^\s*\[tool\.(ruff|black)\b§)
(defregex +ci-run+ #r§^(\s*)(?:-\s+)?run:\s*(.*)$§)
(defregex +make-target+ #r§^([A-Za-z0-9][A-Za-z0-9_-]*(?:[ 	]+[A-Za-z0-9][A-Za-z0-9_-]*)*)[ 	]*::?(?:[^=]|$)§)

(defun call-of (x i id)
  "callOf: (values call index) of the tool call id names before the result at i, nil when none."
  (when (string= id "") (return-from call-of nil))
  (loop for j from (1- i) downto 0
        do (let* ((e (entry-at x j)) (msg (entry-message e)))
             (when (and (string= (entry-type e) "message") msg)
               (let ((role (jget msg "role")))
                 (cond ((equal role "user") (return-from call-of nil))
                       ((equal role "assistant")
                        (let ((content (jget msg "content")))
                          (when (vectorp content)
                            (loop for b across content
                                  do (when (and (hash-table-p b) (equal (jget b "type") "toolCall") (equal (jget b "id") id))
                                       (return-from call-of
                                         (values (make-tcall :id id :name (str (jget b "name"))
                                                             :args (let ((a (jget b "arguments"))) (and (hash-table-p a) a))
                                                             :raw (jget b "arguments"))
                                                 j))))))))))))
  nil)

(defun convention-base (path)
  (let ((i (position #\/ path :from-end t))) (if i (subseq path (1+ i)) path)))

(defun ci-commands (lines)
  "ciCommands: a workflow's run commands in order, once each."
  (let ((out '()) (lines (coerce lines 'vector)) (blank (format nil " ~C" #\Tab)))
    (flet ((add (c)
             (let ((c (trim-space c)))
               (when (and (string/= c "") (not (has-prefix c "#")) (not (member-str c out))) (push-end c out)))))
      (let ((n 0) (count (length lines)))
        (loop while (< n count)
              do (let ((m (regex-find-submatch +ci-run+ (aref lines n))))
                   (when m
                     (let ((value (trim-space (third m))))
                       (if (member-str value '("|" "|-" "|+" ">" ">-" ">+"))
                           (let ((indent (length (second m))))
                             (loop while (< (1+ n) count)
                                   do (let ((next (aref lines (1+ n))))
                                        (when (and (string/= (trim-space next) "")
                                                   (<= (- (length next) (length (trim-left next blank))) indent))
                                          (return))
                                        (incf n)
                                        (add next))))
                           (progn
                             (when (and (>= (length value) 2) (find (char value 0) "\"'")
                                        (char= (char value (1- (length value))) (char value 0)))
                               (setf value (subseq value 1 (1- (length value)))))
                             (add value)))))
                   (incf n)))))
    out))

(defun convention-lines (text)
  "conventionLines: text's lines without a read tool's line numbers."
  (mapcar (lambda (l) (regex-replace-all +convention-line-number+ l ""))
          (split (replace-all text (coerce '(#\Return #\Newline) 'string) (string #\Newline)) (string #\Newline))))

(defun workflow-p (path)
  "isWorkflow."
  (let ((base (convention-base path)))
    (and (contains (cat "/" path) "/.github/workflows/") (or (has-suffix base ".yml") (has-suffix base ".yaml")))))

(defun convention-commands (path text)
  "conventionCommands: a workflow's run lines."
  (and (workflow-p path) (ci-commands (convention-lines text))))

(defun convention-of (path text)
  "conventionOf: what a repository file at path sets, \"\" when none."
  (let* ((base (convention-base path))
         (lines (convention-lines text))
         (manager (cdr (assoc base +convention-managers+ :test #'string=)))
         (tool (cdr (assoc base +convention-tools+ :test #'string=))))
    (cond (manager (cat "package manager " manager))
          (tool tool)
          ((string= base ".editorconfig")
           (let ((settings '()))
             (dolist (line lines)
               (let ((m (regex-find-submatch +editorconfig-setting+ line)))
                 (when m
                   (let ((s (cat (second m) "=" (third m))))
                     (unless (member-str s settings) (push-end s settings))))))
             (if settings (cat "editorconfig " (join settings ", ")) "editorconfig")))
          ((string= base "pyproject.toml")
           (let ((parts '()))
             (dolist (line lines)
               (let ((m (regex-find-submatch +pyproject-tool+ line)))
                 (when m
                   (let ((part (if (string= (second m) "black") "formatter black" "linter ruff")))
                     (unless (member-str part parts) (push-end part parts))))))
             (join parts "; ")))
          ((workflow-p path)
           (let ((commands (ci-commands lines)))
             (if commands (cat "CI runs `" (join commands "`; `") "`") "")))
          ((or (member-str base '("Makefile" "makefile" "GNUmakefile")) (has-suffix base ".mk"))
           (let ((targets '()))
             (dolist (line lines)
               (let ((m (regex-find-submatch +make-target+ line)))
                 (when m
                   (dolist (tg (fields (second m)))
                     (unless (member-str tg targets) (push-end tg targets))))))
             (if targets (cat "make targets " (join targets ", ")) "")))
          (t ""))))

(defun x-tool-result (x i)
  "toolResult: a read tool's result read for a repository convention."
  (let ((msg (entry-message (entry-at x i))))
    (when (jtrue-p (jget msg "isError")) (return-from x-tool-result))
    (multiple-value-bind (call at) (call-of x i (str (jget msg "toolCallId")))
      (when (or (null call) (skipped-tool-p x (tcall-name call)) (not (equal (file-tool-state call) "read")))
        (return-from x-tool-result))
      (let ((p "") (text (text-blocks (jget msg "content"))))
        (dolist (arg +path-args+)
          (let ((v (jget (tcall-args call) arg)))
            (when (and (stringp v) (string/= (trim-space v) "")) (setf p v) (return))))
        (when (or (string= p "") (string= (trim-space text) "")) (return-from x-tool-result))
        (let* ((display (path-display x p))
               (what (convention-of display text)))
          (when (string= what "") (return-from x-tool-result))
          (let ((content (cat what " (repo file `" display "`, sha256 " (hash-id text) ")"))
                (earlier (gethash display (x-conventions x))))
            (when (find content earlier :key #'cdr :test #'string=) (return-from x-tool-result))
            (let ((o (make-obs :content content :kind "convention" :tool (tcall-name call) :target display
                               :source "tool-observed" :authority "repo-file")))
              (setf (obs-keys o) (keys-add (obs-keys o) "config" display))
              (dolist (c (convention-commands display text))
                (setf (obs-keys o) (keys-add (obs-keys o) "commands" c)))
              (dolist (c earlier)
                (push-end (car c) (obs-supersedes o))
                (setf (gethash (car c) (x-superseded x)) t))
              (let ((r (x-add x o at i)))
                (setf (obs-id (rec-obs r)) (record-id (rec-obs r)))
                (setf (gethash display (x-conventions x)) (list (cons (obs-id (rec-obs r)) (obs-content (rec-obs r)))))))))))))
