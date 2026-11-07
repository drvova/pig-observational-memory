;;;; /om:status on the image (memory/commands/status.go StatusLines, StatusFigures; the harness's
;;;; lines in memory/harness/status.go, curated.go, layer.go; memory/rules lifecycle.go
;;;; StatusLines, DetectorCountsLine, v2.go DetectorPassLine, proposal_v2.go
;;;; V2UnitsSinceProposal): PiG's command/run om:status and om's om-status
;;;; (docs/lisp-port-status.md). Every line before "── Code ──" is Go's, byte for byte; the code
;;;; section is the image's own: the Lisp layer's definitions and units, the Go code store's
;;;; definitions (listed, never run), and the refusals the image answered (each a Go fallback when
;;;; the client falls back). It reads only: no child starts, nothing is written (the kernel floor's
;;;; record aside, which harness.StoreOf writes in Go too when the settings ask for a lowering).
;;;; A Go layer the image does not read whole (formats, schemas, summaries, detectors) is refused,
;;;; so the Go engine answers.

(in-package "OM.SERVER")

;;; The refusals the image answered (the fallback counter)

(defvar *refusals* (make-hash-table :test 'equal :synchronized t) "Refusal text -> count, since the image started.")

(defun note-refusal (what)
  "Counts one not-ported answer (jsonrpc.lisp not-ported calls it)."
  (incf (gethash what *refusals* 0)))

;;; The hook-in-flight flag (runtime.Runtime.Snapshot's CompactHookInFlight)

(defvar *hooks-in-flight* (make-hash-table :test 'eq :weakness :key :synchronized t))

(defun call-with-hook-in-flight (c fn)
  (sb-ext:atomic-incf (car (or (gethash c *hooks-in-flight*) (setf (gethash c *hooks-in-flight*) (list 0)))))
  (unwind-protect (funcall fn)
    (sb-ext:atomic-decf (car (gethash c *hooks-in-flight*)))))

(defun hook-in-flight-p (c) (let ((cell (gethash c *hooks-in-flight*))) (and cell (plusp (car cell)))))

;;; Inputs

(defstruct (sctx (:conc-name sx-))
  (entries #()) passive (trust :unknown) (project-file nil) (removed '()) (memory-format "tables")
  store (project "") floor-owner (settings-path "")
  compact-in-flight hook-in-flight
  (auto-compaction t) tokens (window 0) (reserve 0) suspension (session-key "")
  (code-lines '()) (session-id ""))

(defun cat* (&rest parts) (apply #'concatenate 'string parts))

(defun plural-s (n) (if (= n 1) "" "s"))

;;; The memory (commands/status.go)

(defun records-lines (entries)
  (let* ((folded (om::fold-ledger entries)) (by-level1 (om::consolidated-observation-ids entries))
         (by (make-hash-table :test 'equal)))
    (dolist (o (om::folded-observations folded))
      (if (or (gethash (om::obs-id o) by-level1) (string= (om::obs-origin o) "model"))
          (incf (gethash "model" by 0))
          (incf (gethash (om::obs-origin o) by 0))))
    (let ((line (format nil "Records: ~D recorded (~D by code, ~D by the model" (length (om::folded-observations folded))
                        (gethash "code" by 0) (gethash "model" by 0))))
      (let ((n (gethash "host" by 0)))
        (when (plusp n) (setf line (format nil "~A, ~D host ~A" line n (if (= n 1) "summary" "summaries")))))
      (let ((n (gethash "" by 0)))
        (when (plusp n) (setf line (format nil "~A, ~D by memory v1's workers" line n))))
      (append
       (list (format nil "~A) / ~D dropped / ~D active" line (hash-table-count (om::folded-dropped folded))
                    (length (om::folded-active folded)))
            (format nil "Reflections: ~D recorded / ~D standing" (length (om::folded-reflections folded))
                    (length (om::active-reflections (om::folded-reflections folded)))))
       (aggregate-lines folded entries)
       (held-lines folded entries)))))

(defun aggregate-lines (folded entries)
  "commands.aggregateLines: the records by kind and by label, then the sleep line."
  (if (null (om::folded-observations folded))
      (list (sleep-status-line entries))
      (let ((kinds (make-hash-table :test 'equal)) (labels (make-hash-table :test 'equal)) (names '()))
        (dolist (o (om::folded-observations folded))
          (incf (gethash (if (string= (om::obs-kind o) "") "untyped" (om::obs-kind o)) kinds 0)))
        (maphash (lambda (k v) (declare (ignore v)) (push k names)) kinds)
        (maphash (lambda (k l) (declare (ignore k)) (incf (gethash l labels 0))) (om::folded-labels folded))
        (list (cat* "Records by kind: " (om::join (loop for k in (sort names #'string<) collect (format nil "~A ~D" k (gethash k kinds))) ", "))
              (cat* "Records by label: " (om::join (loop for l in '("believed" "superseded" "retracted" "contested" "held")
                                                       collect (format nil "~A ~D" l (gethash l labels 0)))
                                                 ", "))
              (sleep-status-line entries)))))

(defun sleep-status-line (entries)
  "commands.sleepStatusLine."
  (let ((runs (loop for e across (coerce entries 'simple-vector) for d = (om::sleep-data e) when d collect d)))
    (if (null runs)
        "Sleep: no run on this branch"
        (let* ((st (om::sleep-state-of (coerce entries 'simple-vector))) (last (car (last runs)))
               (links (loop for d in runs sum (length (om::sleep-items d "links"))))
               (decayed (loop for d in runs sum (length (om::jstrs (om::jget d "decayed")))))
               (challenged (let ((n 0)) (maphash (lambda (k c) (declare (ignore k)) (when (equal (om::jget c "verdict") "challenged") (incf n)))
                                                 (om::sstate*-checks st))
                             n)))
          (format nil "Sleep: ~D run~A (latest ~A, through ~A): ~D duplicates hidden, ~D links, ~D decayed, ~D verified, ~D retired, ~D checks (~D challenged)"
                  (length runs) (plural-s (length runs)) (om::jget last "trigger") (om::jget last "throughEntryId")
                  (hash-table-count (om::sstate*-duplicates st)) links decayed
                  (hash-table-count (om::sstate*-verified st)) (hash-table-count (om::sstate*-retired st))
                  (hash-table-count (om::sstate*-checks st)) challenged)))))

(defun held-lines (folded entries)
  "commands.heldLines: the active records and standing reflections held because they weaken
security, each with its rule, its content (redacted, on one line) and its anchor."
  (let ((positions (om::entry-index-by-id entries)) (by-id (om::folded-by-id folded)) (lines '()))
    (flet ((held (rule content anchor)
             (push (cat* om::+insecure-held-notice+ " (" rule "): " (om::one-line (om::redact-secrets content)) " (" (or anchor "") ")")
                   lines)))
      (dolist (o (om::folded-active folded))
        (let ((rule (om::held-rule o)))
          (when rule (held rule (om::obs-content o) (om::observation-anchor o positions)))))
      (dolist (r (om::active-reflections (om::folded-reflections folded)))
        (let ((rule (om::held-reflection r by-id)))
          (when rule (held rule (om::refl-content r) (om::reflection-anchor r by-id positions))))))
    (nreverse lines)))

(defun jint (h key) (let ((v (and (hash-table-p h) (gethash key h)))) (if (realp v) (truncate v) 0)))
(defun jbool (h key) (and (hash-table-p h) (eq (gethash key h) :true)))

(defun last-compaction-line (entries)
  (let ((index (om::find-last-compaction-index entries)))
    (when (< index 0) (return-from last-compaction-line nil))
    (let* ((d (om::memory-details-from (om::entry-details (aref entries index))))
           (r (and d (om::details-consolidation d))))
      (if (or (null d) (string/= (om::details-engine d) "v2") (null r))
          "Last compaction: not written by memory v2"
          (let* ((requests (if (jbool r "requested") 1 0))
                 (line (format nil "Last compaction: level ~D, ~D request~A" (if (jbool r "applied") 1 0) requests
                               (if (= requests 1) "" "s"))))
            (cond ((jbool r "applied")
                   (setf line (format nil "~A; level 1 recorded ~D labelled records, ~D merges, ~D reflections, ~D aliases, dropped ~D replaced records"
                                      line (jint r "records") (jint r "merges") (jint r "reflections") (jint r "aliases") (jint r "dropped")))
                   (when (plusp (+ (jint r "confirmed") (jint r "rejected")))
                     (setf line (format nil "~A, settled ~D possible updates (~D confirmed, ~D rejected)" line
                                        (+ (jint r "confirmed") (jint r "rejected")) (jint r "confirmed") (jint r "rejected"))))
                   (when (plusp (jint r "cited"))
                     (setf line (format nil "~A, cited ~D curated memories (~D staged made live)" line (jint r "cited") (jint r "promoted"))))
                   (when (jbool r "harnessOffered")
                     (setf line (format nil "~A, held ~D harness definitions" line (jint r "defined"))))
                   (when (plusp (jint r "refused"))
                     (setf line (format nil "~A, refused ~D proposals" line (jint r "refused")))))
                  ((jbool r "requested")
                   (setf line (cat* line "; level 1 failed (" (om::str (gethash "reason" r)) "), level 0 stands")))
                  (t (setf line (cat* line "; level 1 not run: " (om::str (gethash "reason" r))))))
            line)))))

;;; Curated memory (harness/curated.go CuratedStatusLines, curated.Store.Refused)

(defun refused-memories (dir project)
  (loop for name in (om.layer::memory-file-names dir)
        for reason = (handler-case
                         (multiple-value-bind (m unknown) (om.layer::read-memory dir name)
                           (if (or (not (om.layer::visible-p m project)) (not (om.layer::zero-time-p (om.layer::mem-retired m))))
                               nil
                               (progn (om.layer::load-check m unknown) nil)))
                       (om.layer::not-found () nil)
                       (om.layer::curated-error (e) (princ-to-string e)))
        when reason collect (cons name reason)))

(defun curated-lines (store project &optional (session ""))
  (let* ((memories (om.layer::list-memories store project)) (usage (om.layer::curated-usage store))
         (live 0) (staged 0) (pinned 0) (flagged '()))
    (dolist (m memories)
      (if (om.layer::mem-staged m) (incf staged) (incf live))
      (when (om.layer::mem-pinned m) (incf pinned))
      (when (om.layer::curated-flagged-p m (om.layer::usage-of usage (om.layer::mem-name m)))
        (push (om.layer::mem-name m) flagged)))
    (append (list "── Curated memory ──"
                  (format nil "Memories: ~D live (~D pinned), ~D staged (out of the index until evidence confirms them)" live pinned staged))
            (when flagged
              (list (cat* "Flagged by a user correction since their last edit (ranked last): " (om::join (nreverse flagged) ", "))))
            (loop for (name type description rule) in (handler-case (om.layer::held-memories store project) (error () '()))
                  collect (cat* om::+insecure-held-notice+ " (" rule "): " name " [" type "]: " (om::redact-secrets description)))
            (let ((staged (handler-case (om.layer::staged-changes store project) (error () '()))))
              (when staged
                (list (format nil "Staged changes to shared memory, waiting for a user's words (~D; a staged forget or edit applies in its own session only, a staged layer change nowhere): ~A"
                              (length staged) (om::join (mapcar #'om.layer::staged-change-string staged) "; ")))))
            (let ((refused (refused-memories store project)))
              (when refused
                (list (format nil "Refused by the validator, so not loaded (~D; edit within it or forget): ~A" (length refused)
                              (om::join (loop for (name . reason) in refused collect (cat* name " (" reason ")")) "; ")))))
            (core-lines store session))))

(defun core-lines (store session)
  "harness.CoreStatusLines: the core log read only, none when the store holds none yet."
  (let ((core (om.layer::core-dir store)))
    (when (om.layer::core-exists-p core)
      (multiple-value-bind (through ok) (om.layer::core-consumed core session)
        (om.layer::core-status-lines (om.layer::core-read core) through ok)))))

;;; The harness (harness/status.go, rules)

(defun rules-status-lines (store)
  "rules.StatusLines(store, nil)."
  (let* ((all (om.layer::of-v2 (om.layer::list-rules store))) (active 0) (lines '()))
    (dolist (r all)
      (when (om.layer::rule-active-p r)
        (incf active)
        (let ((applies (om.layer::rule-model r)))
          (when (om.layer::rule-default-p r)
            (setf applies "all models")
            (when (om.layer::rule-excluded r) (setf applies (cat* applies " but " (om::join (om.layer::rule-excluded r) ", ")))))
          (push (format nil "  ~A ~A for ~A: ~A" (om.layer::rule-describe r) (om.layer::rule-state r) applies (om.layer::rule-counts r)) lines))))
    (let ((header (format nil "Rules: ~D active" active)) (retired (- (length all) active)))
      (when (plusp retired) (setf header (format nil "~A, ~D retired" header retired)))
      (cons header (nreverse lines)))))

(defparameter +level1-counts+ '("level1-refused" "level1-specifics-refused" "level1-union-kept" "level1-failed"))

(defun detector-counts-line (store model)
  (let* ((events (om.layer::model-events store model)) (ev (coerce events 'simple-vector))
         (passes (om.layer::passes-from events 0)))
    (when passes
      (when (> (length passes) om.layer::+min-evidence+) (setf passes (last passes om.layer::+min-evidence+)))
      (let ((parts (loop for d in (append om.layer::+v2-detectors+ +level1-counts+)
                         for count = (om.layer::window ev passes d)
                         when (plusp count) collect (format nil "~A ~D" d count))))
        (format nil "Detector counts for ~A over its last ~D compactions (~D tokens): ~A" model (length passes)
                (nth-value 1 (om.layer::window ev passes ""))
                (if parts (om::join parts ", ") "none"))))))

(defun detector-pass-line (store model)
  (let ((e (find-if (lambda (e) (and (string= (om.layer::ev-string e "kind") "detector-pass")
                                     (string= (om.layer::ev-string e "engine") "v2")))
                    (reverse (om.layer::model-events store model)))))
    (when e
      (let* ((counts (sort (loop for (k . n) in (om.layer::ev-counts e "counts")
                                 when (om::has-prefix k "defined:") collect (format nil "~A ~D" (subseq k 8) n))
                           #'string<))
             (line (cat* (format nil "Detector pass after the compaction at ~A: " (om.layer::ev-string e "cut"))
                         (if counts (om::join counts ", ") "no count")))
             (errors (let ((h (gethash "errors" e)))
                       (and (hash-table-p h) (sort (loop for k being the hash-keys of h using (hash-value v) collect (cons k (om::str v)))
                                                   #'string< :key #'car))))
             (pending (om.layer::ev-list e "pending")))
        (when errors
          (setf line (cat* line "; errors (a held one is reverted at the next compaction, a written one flagged): "
                           (om::join (loop for (k . v) in errors collect (cat* k ": " v)) "; "))))
        (when pending (setf line (cat* line "; smoke runs of level 1's proposals: " (om::join (mapcar #'om::str pending) ", "))))
        line))))

(defun code-calls-line (store model)
  (let ((calls 0) (errs 0) (legacy 0))
    (dolist (e (om.layer::model-events store model))
      (when (string= (om.layer::ev-string e "kind") "code")
        (incf calls)
        (when (string= (om.layer::ev-string e "outcome") "error") (incf errs))
        (when (eq (gethash "legacy" e) :true) (incf legacy))))
    (when (plusp calls)
      (format nil "Code calls: ~D (~D errors, ~D queries of the removed language)" calls errs legacy))))

(defun profile-figures (p memory-format)
  (format nil "codec ~A, layout ~A, summary ~A" (om.layer::hp-codec p) (om.layer::summary-format p memory-format)
          (om.layer::scale-text (om.layer::profile-halvings p))))

(defun units-since-proposal (store model)
  "rules.V2UnitsSinceProposal: (values units offered)."
  (let* ((events (om.layer::model-events store model)) (last (om.golayer::last-proposal-events store model))
         (from (if last (min last (length events)) 0)))
    (values (length (om.layer::passes-from events from)) (and last t))))

(defun profile-details (store model p)
  (multiple-value-bind (units offered) (units-since-proposal store model)
    (cons (format nil "Evidence: ~D compactions counted ~A" units
                  (if offered "since the last level-1 offer" "since the start (no level-1 offer yet)"))
          (mapcar #'om.layer::switch-text (om.layer::hp-switches p)))))

(defun model-profile* (store model)
  (when (om.layer::declared-format-p store model)
    (error 'om.golayer::layer-not-ported :what (format nil "format definitions for ~A" model)))
  (om.layer::model-profile store model))

(defun model-status-lines (store model memory-format)
  (let ((p (model-profile* store model)))
    (append (list (format nil "Profile (~D events): ~A" (om.layer::hp-events p) (profile-figures p memory-format)))
            (profile-details store model p)
            (remove nil (list (detector-counts-line store model) (detector-pass-line store model) (code-calls-line store model)))
            (om.golayer::layer-status-lines store model))))

(defun usage-models (store)
  "curated.Store.UsageModels with the layer's own models (but every model's), sorted."
  (let ((models (loop for (name . dirp) in (om.golayer::list-dir (om.layer::join-path store "models"))
                      for base = (and (not dirp) (om::has-suffix name ".jsonl") (subseq name 0 (- (length name) 6)))
                      for model = (and base (om.layer::model-of-dir base))
                      when (and model (om.layer::model-events store model)) collect model)))
    (dolist (m (om.layer::go-models store))
      (unless (or (string= m "*") (member m models :test #'string=)) (push m models)))
    (sort models #'string<)))

(defun shared-harness-lines (x)
  (let ((store (sx-store x)))
    (append (list "── Harness ──")
            (rules-status-lines store)
            (list (progn (floor-of (sx-floor-owner x) store (sx-settings-path x))
                         (floor-status (sx-floor-owner x) store)))
            (om.golayer::layer-status-lines store "*")
            (let ((line (om.golayer::retired-status-line store))) (and line (list line))))))

;;; The report

(defun project-settings-line (x)
  (when (and (not (eq (sx-trust x) :trusted)) (sx-project-file x))
    (if (eq (sx-trust x) :unknown)
        "Project settings ignored: the project trust check failed, so only global settings apply"
        "Project settings ignored: the project is not trusted, so only global settings apply (a /trust decision applies after PiG restarts)")))

(defun pct (current total)
  (if (<= total 0) 0 (floor (+ (* (/ (float current 1d0) total) 100) 0.5d0))))

(defun compaction-line (x)
  (let ((against "PiG's threshold unknown (no context window)"))
    (when (plusp (sx-window x))
      (let* ((limit (- (sx-window x) (sx-reserve x)))
             (context (if (sx-tokens x)
                          (format nil "context ~~~A (~D%)" (om::locale-int (sx-tokens x)) (pct (sx-tokens x) limit))
                          "context unknown until the next response reports usage")))
        (setf against (format nil "PiG's threshold ~A (window ~A - reserveTokens ~A), ~A" (om::locale-int limit)
                              (om::locale-int (sx-window x)) (om::locale-int (sx-reserve x)) context))))
    (if (sx-auto-compaction x)
        (cat* "Compaction: PiG's auto-compaction is on; " against)
        (cat* "Compaction: PiG's auto-compaction is off; only the extension's level-0 compaction runs, at " against))))

(defun status-figures (x)
  "commands.StatusFigures as its String: the model-independent lines, then one block per model."
  (let ((lines '()) (store (sx-store x)))
    (flet ((add (&rest ls) (setf lines (append lines ls))))
      (when (sx-passive x)
        (add "── Mode ──" "Passive: turn-end extraction, level 1, thinning and the extension's compaction trigger are off; PiG's compactions, commands and recall remain active" ""))
      (let ((settings (remove nil (list (project-settings-line x)
                                        (and (sx-removed x) (cat* "Removed settings, ignored: " (om::join (sx-removed x) ", ")))))))
        (when settings (apply #'add (append (list "── Settings ──") settings (list "")))))
      (apply #'add "── Memory ──" (records-lines (sx-entries x)))
      (let ((line (last-compaction-line (sx-entries x)))) (when line (add line)))
      (when (or (sx-compact-in-flight x) (sx-hook-in-flight x))
        (add "" "── In flight ──")
        (when (sx-compact-in-flight x) (add "Auto-compaction: running"))
        (when (sx-hook-in-flight x) (add "Compaction hook: running")))
      (when store
        (apply #'add "" (curated-lines store (sx-project x) (sx-session-id x)))
        (apply #'add "" (shared-harness-lines x))
        (dolist (m (usage-models store))
          (add (cat* "model " m ":"))
          (dolist (l (model-status-lines store m (sx-memory-format x))) (add (cat* "  " l))))))
    lines))

(defun status-lines (x)
  "commands.statusLines."
  (let ((lines (status-figures x)))
    (setf lines (append lines (list "" "── Compaction ──" (compaction-line x))))
    (when (sx-suspension x)
      (setf lines (append lines (list (format nil "Compaction trigger suspended: ~A; asks again once the branch grows" (sx-suspension x))))))
    (when (and (sx-store x) (string/= (sx-session-key x) ""))
      (let* ((key (sx-session-key x)) (p (model-profile* (sx-store x) key)))
        (setf lines (append lines (list "" "── Session model ──"
                                        (format nil "Profile (session model ~A, ~D events): ~A" key (om.layer::hp-events p)
                                                (profile-figures p (sx-memory-format x))))
                            (mapcar (lambda (l) (cat* "  " l)) (profile-details (sx-store x) key p))))))
    (when (sx-code-lines x)
      (setf lines (append lines (list "" "── Code ──") (sx-code-lines x))))
    lines))

;;; The code section, redefined for the Lisp layer (docs/lisp-port-status.md)

(defun lisp-layer-models ()
  (let ((root (om.layer::layer-root)))
    (sort (loop for (name . dirp) in (om.golayer::list-dir (string-right-trim "/" root))
                for model = (and dirp (om.layer::model-of-dir name))
                when model collect model)
          #'string<)))

(defun lisp-layer-lines (model)
  "One model's Lisp layer: its generation, settled and held definitions (from disk, never
compiled into a running child, never reverted here), the reverted ones and the retire rule's units."
  (let* ((manifest (om.layer::read-manifest model))
         (states (om.layer::replay manifest)) (state (aref states (1- (length states))))
         (held (sort (om.layer::dir-file-names (om.layer::join-path (om.layer::model-root model) "held") ".lisp") #'string<))
         (reverted (loop for g in manifest
                         when (equal (om::jget g "op") "revert")
                           collect (format nil "~A (~A)" (om::str (om::jget g "name")) (om::str (om::jget g "reason")))))
         (units (let ((tally (make-hash-table :test 'equal)))
                  (dolist (u (om.layer::read-model-events (om.layer::units-path model)))
                    (let ((cell (or (gethash (om::str (om::jget u "name")) tally)
                                    (setf (gethash (om::str (om::jget u "name")) tally) (list 0 0)))))
                      (if (equal (om::jget u "outcome") "pass") (incf (first cell)) (incf (second cell)))))
                  (sort (loop for k being the hash-keys of tally using (hash-value v)
                              collect (format nil "~A ~D pass, ~D fail" k (first v) (second v)))
                        #'string<))))
    (when (or manifest held)
      (remove nil
              (list (format nil "Lisp layer (~A): generation ~D, settled: ~A; held: ~A" model (om.layer::manifest-generation manifest)
                            (if state (om::join (sort (mapcar #'car (copy-list state)) #'string<) ", ") "none")
                            (if held (om::join held ", ") "none"))
                    (and reverted (cat* "  reverted: " (om::join reverted "; ")))
                    (and units (cat* "  units: " (om::join units "; "))))))))

(defun refusal-lines ()
  (let ((all (sort (loop for k being the hash-keys of *refusals* using (hash-value v) collect (cons k v)) #'string< :key #'car)))
    (list (if all
              (format nil "Refusals the image answered (each a Go fallback when the client falls back): ~D (~A)"
                      (reduce #'+ all :key #'cdr)
                      (om::join (loop for (k . v) in all collect (format nil "~A: ~D" k v)) "; "))
              "Refusals the image answered (each a Go fallback when the client falls back): none"))))

(defun code-lines (config-home project)
  "The code section: every model's Lisp layer, the Go code store's definitions (never run), the
last default an adaptable function fell back to, and the refusal counter."
  (append (or (loop for m in (lisp-layer-models) append (lisp-layer-lines m))
              (list "Lisp layer: no definitions"))
          (let ((units (om.layer::code-store-units (om.layer::code-store-dir config-home project))))
            (list (if units
                      (format nil "Go code store (listed, never run): ~D: ~A" (length units) (om::join units ", "))
                      "Go code store (listed, never run): none")))
          (let ((f om.layer::*last-fallback*))
            (and f (list (format nil "Last default fallback: ~A for ~A (~A)" (second f) (first f) (third f)))))
          (refusal-lines)))

;;; PiG: command/run om:status (commands.RunStatus)

(defun pig-status-text (c)
  (ensure-config c)
  (let* ((cfg (conn-cfg c)) (cwd (scope-cwd)) (trust (rcfg-trust cfg))
         (entries (handler-case (get-branch c)
                    (request-cancelled (e) (error e))
                    (host-call-error (e) (rpc-fail +host-error+ "~A" (host-call-error-message e)))
                    (peer-closed (e) (error e))))
         (home (host-string "host/configHome"))
         (store (and (rcfg-curated-memory cfg) (string/= home "") (store-dir-of home)))
         (model (session-model)))
    (multiple-value-bind (agent-dir dir-name) (conn-paths c)
      (declare (ignore agent-dir))
      (multiple-value-bind (tokens window reserve) (read-threshold c cfg)
        (let ((x (make-sctx :entries entries :passive (rcfg-passive cfg) :trust trust
                            :project-file (project-settings-file-p cwd dir-name)
                            :removed (rcfg-removed cfg) :memory-format (rcfg-memory-format cfg)
                            :store store :project (om.layer::project-root cwd)
                            :floor-owner c :settings-path (conn-settings-path c)
                            :compact-in-flight (compact-in-flight-p c) :hook-in-flight (hook-in-flight-p c)
                            :auto-compaction (pi-auto-compaction-p c cwd trust)
                            :tokens tokens :window window :reserve reserve
                            :suspension (let ((b (active-suspension c entries))) (and b (backoff-reason b)))
                            :session-key (if store (model-key-of model) "")
                            :code-lines (code-lines home (om.layer::project-root cwd))
                            :session-id (host-string "host/sessionId"))))
          (om::join (status-lines x) (string #\Newline)))))))

(defun project-settings-file-p (cwd dir-name)
  "Whether <cwd>/<dir>/settings.json is a regular file (config.Load's ProjectSettingsIgnored)."
  (let ((path (format nil "~A/~A/settings.json" (string-right-trim "/" cwd) dir-name)))
    (handler-case (sb-posix:s-isreg (sb-posix:stat-mode (sb-posix:stat path))) (error () nil))))

(defun status-or-refuse (thunk)
  (handler-case (funcall thunk)
    (om.golayer::layer-not-ported (e) (not-ported (format nil "/om:status over ~A" e)))))

(define-method "command/run" (params)
  ;; Server.handle's MethodCommand: /om:view and /om:status run on the image.
  (with-event (c params)
    (let ((name (om::str (om::jget params "name"))))
      (cond ((string= name "om:view") (run-view c (om::str (om::jget params "args"))) (om::jobject))
            ((string= name "om:status")
             (let ((text (status-or-refuse (lambda () (pig-status-text c)))))
               (host-notify text "info")
               (om::jobject)))
            (t (rpc-fail +invalid-params+ "unknown command: ~A" name))))))

;;; om: om-status (cli.go's status over the neutral host)

(defun om-status-text (s params format)
  "commands.StatusLines over a neutral host: the root's settings, no project trust, no session
model, no context usage, a fresh runtime (nothing in flight, no suspension)."
  (let* ((cfg (make-rcfg)) (cwd (param-string params "cwd"))
         (settings (format nil "~A/settings.json" (string-right-trim "/" *root*))))
    (setf (rcfg-trust cfg) :untrusted)
    (apply-settings cfg settings)
    (setf (rcfg-passive cfg) (passive-p params))
    (let* ((store (and (rcfg-curated-memory cfg) (curated-dir)))
           (auto (let ((v (om::jget (pig-compaction-object settings) "enabled"))) (if (member v '(:true :false)) (eq v :true) t)))
           (x (make-sctx :entries (read-branch s params format) :passive (rcfg-passive cfg) :trust :untrusted
                         :project-file (project-settings-file-p cwd ".om") :removed (rcfg-removed cfg)
                         :memory-format (rcfg-memory-format cfg) :store store :project (om.layer::project-root cwd)
                         :floor-owner (list :om) :settings-path settings :auto-compaction auto
                         :code-lines (code-lines *root* (om.layer::project-root cwd))
                         :session-id (session-id s))))
      (om::join (status-lines x) (string #\Newline)))))

(define-method "om-status" (params)
  ;; om status: the report's text, which the client shows (commands.RunStatus's notify).
  (check-root params)
  (let ((format (check-format params)))
    (with-session (s params)
      (om::jobject "text" (status-or-refuse (lambda () (om-status-text s params format)))))))
