;;;; memory/rules as memory v2 decides them (docs/lisp-core.md decision 7): the rule record
;;;; (rule.go: Signature, newID, encode, parse, list), Check and the catalogue (remedy.go,
;;;; declared.go: CatalogueSeeds, ParseCatalogue, CatalogOf, Tries, Rule), the rules that apply
;;;; to a model (For, ActiveFor, Has), the usage ledger as Go reads it (curated.Store.ModelEvents)
;;;; and evaluate (lifecycle.go), the pure decision of a model's v2 rules from its ledger: probation
;;;; judged by its detector's rate, harness rules written for a detector that counted in each of the
;;;; last MinEvidence compactions, and defaults promoted once established for MinEvidence models.

(in-package "OM.LAYER")

(defparameter +min-evidence+ 3)
(defparameter +default-after-models+ 3)
(defparameter +state-probation+ "probation")
(defparameter +state-established+ "established")
(defparameter +state-retired+ "retired")
(defparameter +proposer-harness+ "harness")
(defparameter +proposer-model+ "model")
(defparameter +unclassified+ "unclassified")
(defparameter +max-text-chars+ 200)
(defparameter +protect-kind-max-fullness+ 2d0)

(defparameter +detect-uncited+ "uncited-assistant-specifics")
(defparameter +detect-cross-scope+ "cross-scope-supersede")
(defparameter +detect-relative-date+ "unresolved-relative-date")
(defparameter +detect-thin-chunk+ "thin-chunk")
(defparameter +detect-dropped-queried+ "dropped-then-queried")
(defparameter +detect-decode-failure+ "decode-failure")
(defparameter +count-codec-decode-failure+ "decode-failure:codec")

(defparameter +detectors+
  (list +detect-uncited+ +detect-cross-scope+ +detect-relative-date+ +detect-thin-chunk+
        +detect-dropped-queried+ +detect-decode-failure+))
(defparameter +v2-detectors+ (list +detect-uncited+ +detect-cross-scope+ +detect-dropped-queried+ +detect-decode-failure+))

(defparameter +remedies+
  '("prompt-line" "require-kind" "keep-head" "block-link" "protect-kind" "split-chunk" "switch-codec" "long-spec"))
(defparameter +remedy-keep-head+ "keep-head")
(defparameter +remedy-switch-codec+ "switch-codec")
(defparameter +v2-remedies+ (list "keep-head" "switch-codec"))

(defparameter +answers+
  (list (cons "prompt-line" +detectors+)
        (cons "require-kind" (list +detect-uncited+))
        (cons "keep-head" (list +detect-uncited+))
        (cons "block-link" (list +detect-cross-scope+))
        (cons "protect-kind" (list +detect-dropped-queried+))
        (cons "split-chunk" (list +detect-thin-chunk+ +detect-uncited+))
        (cons "switch-codec" (list +detect-decode-failure+))
        (cons "long-spec" (list +detect-decode-failure+))))

(defparameter +catalogue+
  (list (cons +detect-uncited+ '("require-kind" "keep-head"))
        (cons +detect-cross-scope+ '("block-link" "prompt-line"))
        (cons +detect-relative-date+ '("prompt-line"))
        (cons +detect-thin-chunk+ '("split-chunk" "prompt-line"))
        (cons +detect-dropped-queried+ '("protect-kind"))
        (cons +detect-decode-failure+ '("switch-codec" "long-spec"))))

(defparameter +catalogue-text+
  (list (cons +detect-uncited+ "Record every concrete thing the assistant produced (names, numbers, lists, recommendations) as a kind=assistant note citing its entry.")
        (cons +detect-cross-scope+ "Never set supersedes between notes whose scopes differ; a figure with another scope is an addition.")
        (cons +detect-relative-date+ "Write the absolute date a relative phrase refers to, computed from the message timestamp, instead of the phrase alone.")
        (cons +detect-thin-chunk+ "Keep the specifics of the chunk (numbers, names, dates) in the notes; a short chunk summary that drops them is not enough.")
        (cons +detect-dropped-queried+ "Do not drop notes whose details the assistant still asks for.")
        (cons +detect-decode-failure+ "Write the rows table exactly in the format the user message describes, or pass the observations array instead.")))

(defparameter +count-revisions+
  (list (cons +detect-uncited+ 2) (cons +detect-dropped-queried+ 2) (cons +detect-decode-failure+ 2)))

(defparameter +rule-fields+
  '("id" "detector" "models" "excluded" "remedy" "kind" "fullness" "text" "state" "reason" "proposer"
    "created" "start" "end" "baseline_events" "baseline_tokens" "after_events" "after_tokens" "after_passes"))

(defmacro push-end-list (item place) `(setf ,place (append ,place (list ,item))))

(defun str-member (s list) (and (member s list :test #'string=) t))

(defun answers-p (remedy detector) (str-member detector (cdr (assoc remedy +answers+ :test #'string=))))

(defun count-revision (key) (or (cdr (assoc key +count-revisions+ :test #'string=)) 1))

(defun kernel-keys () (append +v2-detectors+ (list +count-codec-decode-failure+)))

;;; Floats as strconv writes and reads them

(defun format-fullness (f)
  "formatFullness: \"\" for 0, else strconv.FormatFloat(f, 'f', -1, 64)."
  (if (zerop f) "" (om::go-float-string f)))

(defun parse-decimal (s)
  "strconv.ParseFloat over a plain decimal ([+-]digits[.digits][e[+-]digits]); nil otherwise."
  (let ((i 0) (n (length s)) (sign 1) (int 0) (frac 0) (scale 0) (exp 0) (digits 0))
    (when (and (< i n) (find (char s i) "+-")) (when (char= (char s i) #\-) (setf sign -1)) (incf i))
    (loop while (and (< i n) (digit-char-p (char s i)))
          do (setf int (+ (* int 10) (digit-char-p (char s i)))) (incf i) (incf digits))
    (when (and (< i n) (char= (char s i) #\.))
      (incf i)
      (loop while (and (< i n) (digit-char-p (char s i)))
            do (setf frac (+ (* frac 10) (digit-char-p (char s i)))) (incf scale) (incf i) (incf digits)))
    (when (zerop digits) (return-from parse-decimal nil))
    (when (and (< i n) (char-equal (char s i) #\e))
      (incf i)
      (let ((es 1) (start i))
        (when (and (< i n) (find (char s i) "+-")) (when (char= (char s i) #\-) (setf es -1)) (incf i) (setf start i))
        (loop while (and (< i n) (digit-char-p (char s i))) do (setf exp (+ (* exp 10) (digit-char-p (char s i)))) (incf i))
        (when (= start i) (return-from parse-decimal nil))
        (setf exp (* es exp))))
    (unless (= i n) (return-from parse-decimal nil))
    (* sign (coerce (* (+ (* int (expt 10 scale)) frac) (expt 10 (- exp scale))) 'double-float))))

(defun parse-fullness (s)
  "ParseFullness: (values fullness error)."
  (if (string= s "")
      (values 0d0 nil)
      (let ((f (parse-decimal s)))
        (if (and f (plusp f))
            (values f nil)
            (values 0d0 (format nil "fullness ~S must be a positive number, the pool's active tokens over its target (e.g. 1.5)" s))))))

;;; The rule

(defstruct rule
  (id "") (detector "") (models '()) (excluded '()) (remedy "") (kind "") (fullness 0d0) (text "")
  (state "") (reason "") (proposer "") (created nil) (start 0) (end 0)
  (baseline-events 0) (baseline-tokens 0) (after-events 0) (after-tokens 0) (after-passes 0) (engine ""))

(defun rule-default-p (r) (and (= (length (rule-models r)) 1) (string= (first (rule-models r)) +go-all-models+)))

(defun rule-model (r) (if (or (rule-default-p r) (null (rule-models r))) "" (first (rule-models r))))

(defun rule-applies-to (r model)
  (if (rule-default-p r) (not (str-member model (rule-excluded r))) (str-member model (rule-models r))))

(defun rule-active-p (r) (str-member (rule-state r) (list +state-probation+ +state-established+)))

(defun rule-signature (r)
  (om::join (append (list (rule-detector r) (rule-remedy r) (rule-kind r) (format-fullness (rule-fullness r))
                          (om::to-lower (rule-text r)))
                    (if (string/= (rule-engine r) "") (list (rule-engine r)) '()))
            (string (code-char #x1f))))

(defun rule-describe (r)
  (let ((remedy (rule-remedy r)))
    (cond ((string= remedy "require-kind") (setf remedy (concatenate 'string remedy " kind=" (rule-kind r))))
          ((string= remedy "protect-kind")
           (setf remedy (format nil "~A kind=~A fullness=~A" remedy (rule-kind r) (format-fullness (rule-fullness r)))))
          ((string= remedy "prompt-line") (setf remedy (concatenate 'string remedy " " (om::go-quote (rule-text r))))))
    (format nil "~A (~A → ~A, by ~A)" (rule-id r) (rule-detector r) remedy (rule-proposer r))))

(defun rule-counts (r)
  (let ((out (format nil "before ~D events / ~D tokens" (rule-baseline-events r) (rule-baseline-tokens r))))
    (if (or (plusp (rule-after-passes r)) (string/= (rule-state r) +state-probation+))
        (format nil "~A, since ~D events / ~D tokens over ~D passes" out (rule-after-events r) (rule-after-tokens r) (rule-after-passes r))
        out)))

(defun time-string (time) (format-rfc3339-nano (cons (time-nanos time) 0)))

(defun rule-new-id (r)
  (let ((us (string (code-char #x1f))))
    (om::hash-id (concatenate 'string (rule-signature r) us (om::join (rule-models r) " ") us
                              (format-rfc3339-nano (or (rule-created r) (cons (zero-nanos) 0)))))))

(defun rule-encode (r)
  (let ((row (list (rule-id r) (rule-detector r) (om::join (rule-models r) " ") (om::join (rule-excluded r) " ")
                   (rule-remedy r) (rule-kind r) (format-fullness (rule-fullness r)) (rule-text r) (rule-state r)
                   (rule-reason r) (rule-proposer r) (time-string (rule-created r))
                   (om::itoa (rule-start r)) (om::itoa (rule-end r)) (om::itoa (rule-baseline-events r))
                   (om::itoa (rule-baseline-tokens r)) (om::itoa (rule-after-events r)) (om::itoa (rule-after-tokens r))
                   (om::itoa (rule-after-passes r))))
        (fields +rule-fields+))
    (when (string/= (rule-engine r) "")
      (setf fields (append fields '("engine")) row (append row (list (rule-engine r)))))
    (rows-encode-row "rule" fields row)))

(define-condition rule-refused (error) ((why :initarg :why :reader rule-refused-why))
  (:report (lambda (c s) (write-string (rule-refused-why c) s))))

(defun rule-refuse (fmt &rest args) (error 'rule-refused :why (apply #'format nil fmt args)))

(defun check-rule (r)
  "rules.Check: the rule with its parameters filled in, or rule-refused."
  (unless (str-member (rule-detector r) +detectors+) (rule-refuse "unknown detector ~S" (rule-detector r)))
  (unless (str-member (rule-remedy r) +remedies+) (rule-refuse "unknown remedy ~S" (rule-remedy r)))
  (unless (answers-p (rule-remedy r) (rule-detector r))
    (rule-refuse "~A does not answer ~A" (rule-remedy r) (rule-detector r)))
  (let ((text (om::join (om::fields (rule-text r)) " ")) (remedy (rule-remedy r)))
    (cond ((string= remedy "prompt-line")
           (when (string= text "") (rule-refuse "prompt-line needs text"))
           (when (> (length text) +max-text-chars+) (rule-refuse "a rule's text is one line of at most ~D characters" +max-text-chars+))
           (setf (rule-text r) text))
          ((string/= text "") (rule-refuse "only prompt-line takes text")))
    (cond ((string= remedy "require-kind")
           (when (string= (rule-kind r) "") (setf (rule-kind r) "assistant"))
           (unless (str-member (rule-kind r) om::+observation-kinds+) (rule-refuse "kind ~S" (rule-kind r))))
          ((string= remedy "protect-kind")
           (unless (or (string= (rule-kind r) +unclassified+) (str-member (rule-kind r) om::+observation-kinds+))
             (rule-refuse "kind ~S" (rule-kind r)))
           (when (zerop (rule-fullness r)) (setf (rule-fullness r) +protect-kind-max-fullness+))
           (when (or (minusp (rule-fullness r)) (> (rule-fullness r) +protect-kind-max-fullness+))
             (rule-refuse "protect-kind fullness")))
          ((or (string/= (rule-kind r) "") (/= (rule-fullness r) 0))
           (rule-refuse "~A takes no kind or fullness" remedy))))
  r)

(defun rule-parse (where text)
  "rules.parse: the rule a remedy definition's text holds, or rule-refused."
  (multiple-value-bind (name fields rows)
      (handler-case (rows-decode-table text) (rows-error (e) (rule-refuse "~A: ~A" where e)))
    (unless (and (string= name "rule") (= (length rows) 1)) (rule-refuse "~A is not one rule row" where))
    (let* ((row (first rows)) (cell (lambda (f) (rows-cell fields row f)))
           (num (lambda (f) (or (ignore-errors (parse-integer (funcall cell f))) 0)))
           (r (make-rule :id (funcall cell "id") :detector (funcall cell "detector")
                         :models (om::fields (funcall cell "models")) :excluded (om::fields (funcall cell "excluded"))
                         :remedy (funcall cell "remedy") :kind (funcall cell "kind") :text (funcall cell "text")
                         :state (funcall cell "state") :reason (funcall cell "reason") :proposer (funcall cell "proposer")
                         :start (funcall num "start") :end (funcall num "end")
                         :baseline-events (funcall num "baseline_events") :baseline-tokens (funcall num "baseline_tokens")
                         :after-events (funcall num "after_events") :after-tokens (funcall num "after_tokens")
                         :after-passes (funcall num "after_passes") :engine (funcall cell "engine")
                         :fullness (or (parse-decimal (funcall cell "fullness")) 0d0)
                         :created (parse-rfc3339 (funcall cell "created")))))
      (when (or (string= (rule-id r) "") (null (rule-models r))) (rule-refuse "~A is not a valid rule" where))
      (check-rule r)
      (unless (str-member (rule-state r) (list +state-probation+ +state-established+ +state-retired+))
        (rule-refuse "~A has an unknown state" where))
      (cond ((string= (rule-engine r) ""))
            ((string= (rule-engine r) "v2")
             (unless (and (str-member (rule-detector r) +v2-detectors+) (str-member (rule-remedy r) +v2-remedies+))
               (rule-refuse "~A does not run under memory v2" where)))
            (t (rule-refuse "~A has an unknown engine" where)))
      r)))

(defun rule-layer-model (r)
  (cond ((rule-default-p r) +go-all-models+)
        ((string/= (rule-model r) "") (rule-model r))
        (t nil)))

(defun rule-created-nanos (r) (time-nanos (rule-created r)))

(defun rules-of (defs)
  "rulesOf: the rules among remedy definitions (gdefs), oldest first."
  (let ((out '()))
    (dolist (d defs)
      (let ((r (handler-case (rule-parse (concatenate 'string "remedy/" (gdef-name d)) (gdef-text d))
                 (rule-refused () nil))))
        (when (and r (string= (rule-id r) (gdef-name d)) (equal (rule-layer-model r) (gdef-model d)))
          (push r out))))
    (stable-sort (nreverse out)
                 (lambda (a b) (if (/= (rule-created-nanos a) (rule-created-nanos b))
                                   (< (rule-created-nanos a) (rule-created-nanos b))
                                   (string< (rule-id a) (rule-id b)))))))

(defun list-rules (store)
  "rules.list: every written rule in the store's layer, oldest first."
  (rules-of (loop for m in (go-models store) append (go-list-remedies store m))))

(defun of-v2 (rules) (remove-if-not (lambda (r) (string= (rule-engine r) "v2")) rules))

(defun active-rules (store model)
  "rules.For(store, model).Rules: memory v2's active rules that apply to model, from its
effective layer (held over written, the model's own over all models')."
  (if (or (null model) (string= model ""))
      '()
      (remove-if-not (lambda (r) (and (rule-active-p r) (rule-applies-to r model)))
                     (of-v2 (rules-of (mapcar #'cdr (go-effective-remedies store model)))))))

(defun rules-have-p (rules remedy) (and (find remedy rules :key #'rule-remedy :test #'string=) t))

;;; The catalogue (declared.go)

(defstruct centry remedy detector (rank 0) (kind "") (fullness 0d0) (text "") (authored nil))

(defun catalogue-seed (remedy)
  "catalogueSeed: remedy's shipped catalogue table."
  (let ((rows '()) (absent '()))
    (dolist (detector +detectors+)
      (when (answers-p remedy detector)
        (let* ((rank (let ((p (position remedy (cdr (assoc detector +catalogue+ :test #'string=)) :test #'string=)))
                       (if p (1+ p) 0)))
               (row (list detector (om::itoa rank) "" "" "")))
          (cond ((string= remedy "prompt-line") (setf (fifth row) (cdr (assoc detector +catalogue-text+ :test #'string=))))
                ((string= remedy "require-kind") (setf (third row) "assistant")))
          (unless (and (zerop rank) (string/= remedy "prompt-line"))
            (push-end-list row rows)
            (push-end-list (loop for v in row for i from 0 collect (and (> i 0) (string= v ""))) absent)))))
    (om::encode-table (om::make-rtable :name "catalogue" :fields '("detector" "rank" "kind" "fullness" "text")
                                       :rows rows :absent absent))))

(defvar *catalogue-seeds* nil)

(defun catalogue-seeds ()
  "CatalogueSeeds: remedy name -> its shipped catalogue text."
  (or *catalogue-seeds*
      (setf *catalogue-seeds*
            (let ((h (make-hash-table :test 'equal)))
              (dolist (remedy +remedies+ h) (setf (gethash remedy h) (catalogue-seed remedy)))))))

(defun parse-catalogue (remedy text)
  "ParseCatalogue: (values entries error)."
  (block nil
    (unless (str-member remedy +remedies+) (return (values nil "not a catalogue remedy")))
    (multiple-value-bind (name fields rows)
        (handler-case (rows-decode-table (om::trim-space text)) (rows-error (e) (return (values nil (princ-to-string e)))))
      (unless (string= name "catalogue") (return (values nil "not a catalogue table")))
      (dolist (c fields)
        (unless (str-member c '("detector" "rank" "kind" "fullness" "text")) (return (values nil "unknown column"))))
      (let ((out '()) (seen '()))
        (dolist (row rows)
          (let* ((cell (lambda (c) (om::trim-space (rows-cell fields row c))))
                 (e (make-centry :remedy remedy :detector (funcall cell "detector") :kind (funcall cell "kind")
                                 :text (funcall cell "text"))))
            (when (str-member (centry-detector e) seen) (return (values nil "a detector twice")))
            (push (centry-detector e) seen)
            (let ((rank (funcall cell "rank")))
              (when (string/= rank "")
                (let ((n (ignore-errors (parse-integer rank))))
                  (unless (and n (>= n 0)) (return (values nil "bad rank")))
                  (setf (centry-rank e) n))))
            (multiple-value-bind (f err) (parse-fullness (funcall cell "fullness"))
              (when err (return (values nil err)))
              (setf (centry-fullness e) f))
            (let ((probe (make-rule :detector (centry-detector e) :remedy remedy :kind (centry-kind e)
                                    :fullness (centry-fullness e) :text (centry-text e))))
              (when (and (string= remedy "protect-kind") (string= (rule-kind probe) ""))
                (setf (rule-kind probe) +unclassified+))
              (handler-case (check-rule probe) (rule-refused (c) (return (values nil (rule-refused-why c))))))
            (push e out)))
        (values (nreverse out) nil)))))

(defun validate-remedy (d)
  "rules.validateRemedy over a gdef: t when it passes."
  (let ((catalogue (handler-case (multiple-value-bind (name) (rows-decode-table (om::trim-space (gdef-text d)))
                                   (string= name "catalogue"))
                     (rows-error () nil))))
    (if (not catalogue)
        (not (str-member (gdef-name d) +remedies+))
        (multiple-value-bind (entries err) (parse-catalogue (gdef-name d) (gdef-text d))
          (declare (ignore entries))
          (null err)))))

(defun catalog-of (effective)
  "CatalogOf over the effective remedy definitions (an alist name -> gdef): detector -> its
entries, by rank then catalogue order."
  (let ((c (make-hash-table :test 'equal)))
    (dolist (remedy +remedies+)
      (let* ((seeds (parse-catalogue remedy (gethash remedy (catalogue-seeds))))
             (d (cdr (assoc remedy effective :test #'string=)))
             (entries nil))
        (when d
          (multiple-value-bind (es err) (parse-catalogue remedy (gdef-text d))
            (unless err
              (dolist (e es)
                (setf (centry-authored e)
                      (and (string/= (centry-text e) "")
                           (string/= (centry-text e)
                                     (let ((s (find (centry-detector e) seeds :key #'centry-detector :test #'string=)))
                                       (if s (centry-text s) ""))))))
              (setf entries (or es :none)))))
        (when (or (null entries) (eq entries :none)) (setf entries (if (eq entries :none) '() seeds)))
        (dolist (e entries) (push-end-list e (gethash (centry-detector e) c)))))
    (maphash (lambda (k v)
               (setf (gethash k c)
                     (stable-sort (copy-list v)
                                  (lambda (a b) (if (/= (centry-rank a) (centry-rank b))
                                                    (< (centry-rank a) (centry-rank b))
                                                    (< (or (position (centry-remedy a) +remedies+ :test #'string=) 99)
                                                       (or (position (centry-remedy b) +remedies+ :test #'string=) 99)))))))
             c)
    c))

(defun catalog-tries (c detector)
  (loop for e in (gethash detector c) when (plusp (centry-rank e)) collect (centry-remedy e)))

(defun most-frequent (kinds)
  "mostFrequent over an alist kind -> n."
  (let ((best +unclassified+) (best-n 0))
    (dolist (k (sort (mapcar #'car kinds) #'string<) best)
      (let ((n (cdr (assoc k kinds :test #'string=))))
        (when (> n best-n) (setf best k best-n n))))))

(defun catalog-rule (c detector remedy kinds)
  (let ((r (make-rule :detector detector :remedy remedy :proposer +proposer-harness+))
        (e (or (find remedy (gethash detector c) :key #'centry-remedy :test #'string=) (make-centry))))
    (cond ((string= remedy "prompt-line")
           (setf (rule-text r) (centry-text e))
           (when (centry-authored e) (setf (rule-proposer r) +proposer-model+)))
          ((string= remedy "require-kind")
           (setf (rule-kind r) (if (string= (centry-kind e) "") "assistant" (centry-kind e))))
          ((string= remedy "protect-kind")
           (setf (rule-kind r) (if (string= (centry-kind e) "") (most-frequent kinds) (centry-kind e))
                 (rule-fullness r) (if (zerop (centry-fullness e)) +protect-kind-max-fullness+ (centry-fullness e)))))
    r))

;;; The usage ledger as curated.Store.ModelEvents reads it

(defun models-path (store model) (join-path store "models" (concatenate 'string (model-dir model) ".jsonl")))

(defun go-int-p (v) (and (realp v) (= v (ftruncate v))))

(defun model-event-shape-p (e)
  "Whether json.Unmarshal into curated.ModelEvent succeeds: every member it has is of its type."
  (flet ((of (key test) (multiple-value-bind (v present) (gethash key e)
                          (or (not present) (eq v :null) (funcall test v))))
         (map-of (test) (lambda (v) (and (hash-table-p v) (loop for x being the hash-values of v always (or (eq x :null) (funcall test x))))))
         (strings (v) (and (vectorp v) (not (stringp v)) (every (lambda (x) (or (stringp x) (eq x :null))) v))))
    (and (every (lambda (k) (of k #'stringp)) '("time" "model" "kind" "outcome" "codec" "target" "tool" "stage" "engine" "cut" "unfinished"))
         (every (lambda (k) (of k (lambda (v) (member v '(:true :false))))) '("syntax" "legacy" "level1"))
         (of "tokens" #'go-int-p)
         (every (lambda (k) (of k (map-of #'go-int-p))) '("counts" "kinds" "revisions"))
         (of "errors" (map-of #'stringp))
         (of "ranHeld" #'strings) (of "pending" #'strings))))

(defun model-events (store model)
  "curated.Store.ModelEvents: the model's complete ledger lines that decode as events of it."
  (let* ((text (read-file (models-path store model)))
         (end (and text (position #\Newline text :from-end t))))
    (when end
      (loop for line in (om::split (subseq text 0 end) (string #\Newline))
            for v = (and (string/= line "") (handler-case (om::json-parse (coerce line 'simple-string)) (error () nil)))
            when (and (hash-table-p v) (model-event-shape-p v)
                      (string/= (ev-string v "kind") "") (string= (ev-string v "model") model))
              collect v))))

(defun ev-count (e key)
  (let ((c (ev e "counts"))) (if (hash-table-p c) (let ((n (gethash key c))) (if (realp n) (truncate n) 0)) 0)))

(defun ev-revision (e key)
  (let ((c (ev e "revisions"))) (if (hash-table-p c) (let ((n (gethash key c))) (if (realp n) (truncate n) 0)) 0)))

(defun compaction-pass-p (e)
  "isCompactionPass: a v2 compaction with tokens that was not empty."
  (and (v2-compaction-p e) (plusp (ev-int e "tokens")) (string/= (ev-string e "outcome") "empty")))

;;; evaluate (lifecycle.go)

(defun counted-now-p (e key)
  (let ((r (ev-revision e key))) (= (if (zerop r) 1 r) (count-revision key))))

(defun passes-from (events from)
  (loop for e in (nthcdr (max 0 from) events) for i from (max 0 from)
        when (compaction-pass-p e) collect i))

(defun counted-passes (ev from key)
  (remove-if-not (lambda (i) (counted-now-p (aref ev i) key)) (passes-from (coerce ev 'list) from)))

(defun count-key (detector remedy)
  (if (and (string= detector +detect-decode-failure+) (string= remedy +remedy-switch-codec+))
      +count-codec-decode-failure+
      detector))

(defun window (ev indexes detector)
  (let ((count 0) (tokens 0))
    (dolist (i indexes) (incf count (ev-count (aref ev i) detector)) (incf tokens (ev-int (aref ev i) "tokens")))
    (values count tokens)))

(defun fell-p (be bt ae at)
  (and (/= be 0) (/= bt 0) (/= at 0) (< (* ae bt) (* be at))))

(defstruct transition rule to added)

(defun transition-message (tr)
  (let ((r (transition-rule tr)))
    (cond ((and (transition-added tr) (rule-default-p r))
           (format nil "Observational memory: rule ~A is now a default for all models (~A)" (rule-describe r) (rule-reason r)))
          ((transition-added tr)
           (format nil "Observational memory: rule ~A added on probation for ~A (~A)" (rule-describe r) (rule-model r) (rule-reason r)))
          (t (format nil "Observational memory: rule ~A is now ~A for ~A (~A)" (rule-describe r) (transition-to tr)
                     (rule-model r) (rule-reason r))))))

(defun evaluate-rules (model events all now catalog)
  "rules.evaluate: (values changed transitions) for model's usage ledger events (a list, in
order), every rule in the store, the time now and the model's catalogue. Pure."
  (let* ((ev (coerce events 'simple-vector))
         (rules (mapcar #'copy-rule (of-v2 all)))
         (changed '()) (transitions '()))
    (flet ((replace-rule (r)
             (let ((p (position (rule-id r) rules :key #'rule-id :test #'string=)))
               (if p (setf (nth p rules) r) (push-end-list r rules)))))
      ;; Probation.
      (dolist (r0 (copy-list rules))
        (when (and (string= (rule-state r0) +state-probation+) (string= (rule-model r0) model)
                   (str-member (count-key (rule-detector r0) (rule-remedy r0)) (kernel-keys)))
          (let* ((key (count-key (rule-detector r0) (rule-remedy r0)))
                 (passes (counted-passes ev (rule-start r0) key)))
            (when (>= (length passes) +min-evidence+)
              (let ((r (copy-rule r0)) (passes (subseq passes 0 +min-evidence+)))
                (multiple-value-bind (ae at) (window ev passes key)
                  (setf (rule-after-events r) ae (rule-after-tokens r) at (rule-after-passes r) (length passes)
                        (rule-end r) (1+ (car (last passes)))))
                (if (fell-p (rule-baseline-events r) (rule-baseline-tokens r) (rule-after-events r) (rule-after-tokens r))
                    (setf (rule-state r) +state-established+
                          (rule-reason r) (format nil "~A fell from ~D events in ~D tokens to ~D in ~D over ~D passes"
                                                  (rule-detector r) (rule-baseline-events r) (rule-baseline-tokens r)
                                                  (rule-after-events r) (rule-after-tokens r) (rule-after-passes r)))
                    (setf (rule-state r) +state-retired+
                          (rule-reason r) (format nil "~A did not fall: ~D events in ~D tokens before, ~D in ~D over ~D passes since"
                                                  (rule-detector r) (rule-baseline-events r) (rule-baseline-tokens r)
                                                  (rule-after-events r) (rule-after-tokens r) (rule-after-passes r))))
                (replace-rule r)
                (push-end-list r changed)
                (push-end-list (make-transition :rule r :to (rule-state r)) transitions))))))
      ;; Harness rules.
      (dolist (detector +v2-detectors+)
        (let ((floor 0) (busy nil) (tried '()))
          (dolist (r rules)
            (when (and (string= (rule-detector r) detector) (rule-applies-to r model))
              (pushnew (rule-remedy r) tried :test #'string=)
              (unless (rule-default-p r)
                (when (string= (rule-state r) +state-probation+) (setf busy t))
                (setf floor (max floor (rule-start r) (rule-end r))))))
          (unless busy
            (let ((passes (counted-passes ev floor detector)))
              (when (>= (length passes) +min-evidence+)
                (let ((last (last passes +min-evidence+)) (rising t) (kinds '()))
                  (dolist (i last)
                    (when (zerop (ev-count (aref ev i) detector)) (setf rising nil) (return))
                    (let ((k (ev (aref ev i) "kinds")))
                      (when (hash-table-p k)
                        (maphash (lambda (kind n)
                                   (when (realp n)
                                     (let ((cell (assoc kind kinds :test #'string=)))
                                       (if cell (incf (cdr cell) (truncate n)) (push (cons kind (truncate n)) kinds)))))
                                 k))))
                  (when rising
                    (let ((remedy ""))
                      (dolist (candidate (catalog-tries catalog detector))
                        (unless (or (str-member candidate tried) (not (str-member candidate +v2-remedies+))
                                    (let ((key (count-key detector candidate)))
                                      (and (string/= key detector) (zerop (window ev last key)))))
                          (setf remedy candidate)
                          (return)))
                      (when (string/= remedy "")
                        (let ((r (handler-case (check-rule (catalog-rule catalog detector remedy kinds))
                                   (rule-refused () nil))))
                          (when r
                            (setf (rule-models r) (list model) (rule-state r) +state-probation+ (rule-created r) now
                                  (rule-start r) (length ev) (rule-engine r) "v2")
                            (multiple-value-bind (be bt) (window ev last (count-key detector remedy))
                              (setf (rule-baseline-events r) be (rule-baseline-tokens r) bt))
                            (setf (rule-reason r) (format nil "~A counted in each of the last ~D passes (~D events in ~D tokens)"
                                                          detector +min-evidence+ (rule-baseline-events r) (rule-baseline-tokens r)))
                            (setf (rule-id r) (rule-new-id r))
                            (replace-rule r)
                            (push-end-list r changed)
                            (push-end-list (make-transition :rule r :to +state-probation+ :added t) transitions))))))))))))
      ;; Defaults.
      (let ((established (make-hash-table :test 'equal)) (retired (make-hash-table :test 'equal))
            (sample (make-hash-table :test 'equal)) (has-default (make-hash-table :test 'equal))
            (authored (make-hash-table :test 'equal)) (order '()))
        (dolist (r rules)
          (let ((sig (rule-signature r)))
            (cond ((rule-default-p r) (setf (gethash sig has-default) t))
                  ((string= (rule-state r) +state-established+)
                   (unless (gethash sig established) (push-end-list sig order))
                   (setf (gethash sig authored) (or (gethash sig authored)
                                                    (and (string= (rule-proposer r) +proposer-model+) (string/= (rule-text r) ""))))
                   (pushnew (rule-model r) (gethash sig established) :test #'string=)
                   (setf (gethash sig sample) r))
                  ((string= (rule-state r) +state-retired+)
                   (push-end-list (rule-model r) (gethash sig retired))))))
        ;; Go ranges over a map here: the order of two defaults promoted by one pass is not fixed
        ;; there; signature order here.
        (dolist (sig (sort order #'string<))
          (let ((models (gethash sig established)))
            (unless (or (gethash sig has-default) (< (length models) +default-after-models+))
              (let* ((base (gethash sig sample))
                     (d (make-rule :detector (rule-detector base) :remedy (rule-remedy base) :kind (rule-kind base)
                                   :fullness (rule-fullness base) :text (rule-text base) :models (list +go-all-models+)
                                   :excluded (gethash sig retired) :state +state-established+
                                   :proposer (if (gethash sig authored) +proposer-model+ +proposer-harness+)
                                   :created now :engine "v2")))
                (setf (rule-reason d) (format nil "established for ~D models" (length models)))
                (setf (rule-id d) (rule-new-id d))
                (replace-rule d)
                (push-end-list d changed)
                (push-end-list (make-transition :rule d :to +state-established+ :added t) transitions)))))))
    (values changed transitions)))

(defun write-rule (store r)
  "rules.write: r in the store's layer, under its model (the caller holds the store's lock)."
  (let ((model (or (rule-layer-model r) (error "rule ~A applies to no model" (rule-id r)))))
    (go-put-remedy store model (rule-id r) (rule-encode r))))
