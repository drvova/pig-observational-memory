;;;; W5: memory v2's compaction hook (memory/hooks/compaction_v2.go, extract_v2.go's
;;;; extractCutV2, appendHostSummaryV2 and dropByRule, consolidate_v2.go's skip reasons,
;;;; config.ResolveV2SummaryBudget, extract.HostSummary) over a host given as closures, so the
;;;; image's server and the tests drive it alike. It keeps the harness's cut exactly, runs level 0
;;;; over whatever of the cut range no turn end read, renders the ledger under the budget the
;;;; compaction's tokensBefore and removed source give, and else the minimal brief. It makes no
;;;; model request: a host that hands no session model (every om path) skips level 1 with Go's
;;;; reason, and a host that hands one is refused by the server before this runs
;;;; (docs/lisp-port-hooks.md).

(in-package "OM")

;;; The host and the event

(defstruct hhost
  "What the hook reads and writes: read returns the merged branch (a vector), append stores a
custom entry after the tip from its type and its data's JSON text and returns the new id."
  read append (cwd "") (skip-tools (list +memory-tool+))
  ;; detect, when set, is the harness's detectV2 (om.layer:harness-detect over the session model
  ;; and store): a function of (entries start end folded batches covers) returning the batches and
  ;; a function that records the run, called once the extraction landed on a fresh branch.
  (detect nil)
  ;; structure, when set, is host.StructureReader (host/structure): a function of a decoded
  ;; host.StructureQuery returning the decoded answer, or nil when the host reads none.
  (structure nil))

;;; Project knowledge (project.lisp; hooks/project_v2.go)

(defun encoding-repo ()
  "The session's repository as *encoding* holds it (repoOfV2): a host.Repo object with its id and
head, nil outside one."
  (let ((frame (getf *encoding* :frame)))
    (when (string/= (alist-member frame "repoId") "")
      (jobject "repoId" (alist-member frame "repoId") "head" (alist-member frame "head")))))

(defun stamp-batches (structure batches)
  "stampV2: the new project records' body hashes from their paths' blobs at HEAD."
  (when structure
    (let ((q (project-stamp-query (loop for b in batches append (batch-observations b)))))
      (unless (project-query-empty-p q)
        (let ((answer (funcall structure q)))
          (when answer
            (dolist (b batches) (project-stamp (batch-observations b) answer))))))))

(defvar *freshness* '()
  "The compaction's freshness verdicts (compaction-freshness), bound around level 1, which offers the
changed ones as possibly stale (stale-records).")

(defun stale-records (structure verdicts)
  "staleRecordsV2: the changed verdicts as stale-recs, at most +project-max-diffs+, each with its
path's diff from the commit it was written at to HEAD, asked in one query."
  (let ((q (project-diff-query verdicts)) (diffs (make-set)) (out '()))
    (when (or (project-query-empty-p q) (null structure)) (return-from stale-records '()))
    (let ((answer (funcall structure q)))
      (dolist (d (jlist-of (jget answer "diffs")))
        (setf (gethash (cat (str (jget d "path")) (string #\Nul) (str (jget d "from"))) diffs) d)))
    (dolist (v verdicts out)
      (when (and (string= (freshness-kind v) "changed") (< (length out) +project-max-diffs+))
        (let ((d (gethash (cat (freshness-path v) (string #\Nul) (freshness-sha v)) diffs)))
          (push-end (make-stale-rec :id (freshness-id v) :path (freshness-path v) :since (freshness-sha v)
                                    :head (freshness-head v) :blob (freshness-blob v)
                                    :diff (if d (str (jget d "diff")) "") :diff-known (and d (jtrue-p (jget d "known")))
                                    :truncated (and d (jtrue-p (jget d "truncated"))))
                    out))))))

(defun compaction-freshness (h entries)
  "freshnessV2: the active project records of entries judged against HEAD, as (values verdicts
repo); none without a view, a repository or a record to ask about."
  (let ((repo (encoding-repo)))
    (when (and (hhost-structure h) repo)
      (let* ((records (folded-active (fold-ledger entries)))
             (q (project-query records repo)))
        (unless (project-query-empty-p q)
          (let ((answer (funcall (hhost-structure h) q)))
            (when answer (values (project-verdicts records repo answer) repo))))))))

(defstruct hconfig
  "The settings the hook reads (config.Config): passive, consolidate, compactionSummaryMaxTokens,
memoryFormat, the working-memory limits (global and per model) and rawFloor."
  (passive nil) (consolidate-off nil) (summary-max 0) (memory-format "tables")
  (limits (list 0 0 0 0 0)) (model-limits nil) (raw-floor nil)
  ;; decay: its clock ("" off, "compactions" or "turns") and horizon (0 for the default).
  (decay "") (decay-horizon 0)
  ;; The sleep pass's compaction fallback (sleepAfterCompactions, 0 never; sleepMaxRecords).
  (sleep-after 4) (sleep-max-records 5000)
  ;; Phase 8: the cap on cues per compaction (cueCap, 0 off) and the dream section (dreams).
  (cue-cap 3) (dreams nil))

;;; config/working.go: working memory's caps, the raw floor and the quality threshold

(defparameter +limit-keys+
  '("workingMemoryConstraints" "workingMemoryScopeExclusions" "workingMemoryActions" "rawFloorTokens" "qualityTriggerTokens")
  "config.limitKeys, in ModelLimits order.")
(defparameter +limit-defaults+ '(10 10 5 32000 64000)
  "The defaults: constraints, scope exclusions, actions, the raw floor and the quality threshold.")

(defun read-limits (object)
  "readLimits: the limit keys of one settings object, 0 for unset."
  (mapcar (lambda (key)
            (let ((v (and (hash-table-p object) (jget object key))))
              (if (and (realp v) (> v 0) (= v (ftruncate v))) (truncate v) 0)))
          +limit-keys+))

(defun limits-over (base over)
  "ModelLimits.over: over's set limits laid over base."
  (mapcar (lambda (b o) (if (> o 0) o b)) base over))

(defun read-model-limits (object into)
  "modelLimits: each \"<provider>/<id>\" entry's set limits laid over into's (a hash table, made
when nil); returns it."
  (when (hash-table-p object)
    (maphash (lambda (key value)
               (let ((limits (read-limits value)))
                 (when (and (hash-table-p value) (some #'plusp limits))
                   (unless into (setf into (make-hash-table :test 'equal)))
                   (setf (gethash key into) (limits-over (gethash key into (list 0 0 0 0 0)) limits)))))
             object))
  into)

(defun hook-model-pair (model)
  "host.ModelProvider and host.ModelID of a session model (a decoded object), \"\" for none."
  (if (hash-table-p model)
      (let ((id (str (jget model "id"))))
        (values (str (jget model "provider")) (if (string/= id "") id (str (jget model "modelId")))))
      (values "" "")))

(defun limits-for (limits model-limits model)
  "Config.LimitsFor: the session model's modelLimits entry, else the global setting, else the
default, field by field: (constraints scope-exclusions actions raw-floor quality-threshold)."
  (multiple-value-bind (provider id) (hook-model-pair model)
    (let ((out (limits-over +limit-defaults+ limits)))
      (if (and model-limits (or (string/= provider "") (string/= id "")))
          (limits-over out (gethash (cat provider "/" id) model-limits (list 0 0 0 0 0)))
          out))))

(defun below-raw-floor-p (raw-floor limits model-limits model tokens-before)
  "BelowRawFloor: the floor is on and tokensBefore is known and below the model's floor."
  (and raw-floor (> tokens-before 0) (< tokens-before (fourth (limits-for limits model-limits model)))))

(defstruct cevent
  "hooks.BeforeCompactEvent with its preparation's members."
  (first-kept "") (tokens-before 0) branch (reason "") (custom-instructions "")
  (messages-to-summarize '()) (turn-prefix '()) (split-turn nil) (previous-summary "")
  (file-ops '()) (reserve 0))

(defstruct cresult summary (first-kept "") (tokens-before 0) (details-json "") (kind "")
  ;; usage is the level-1 request's provider usage (CompactionResult.Usage), nil at level 0.
  (usage nil))

(defun hhost-branch (h) (coerce (funcall (hhost-read h)) 'simple-vector))

(defun synthetic-entry (id custom-type data-json)
  (make-entry :type "custom" :id id :custom-type custom-type :data (json-parse data-json)))

;;; Ranges

(defun source-entries (entries)
  (remove-if-not #'source-entry-p (coerce entries 'list)))

(defun source-tokens (entries)
  (loop for e in entries when (source-entry-p e) sum (estimate-entry-tokens e)))

(defun cut-range (entries event)
  "v2CutRange: from the previous compaction's boundary up to the first kept entry; from the
preparation's messages when that id is not on the branch."
  (let ((start (compaction-range-start-index entries))
        (cut (entry-index-for-id entries (cevent-first-kept event))))
    (if (>= cut start)
        (coerce (subseq entries start cut) 'list)
        (loop for m in (append (cevent-messages-to-summarize event) (cevent-turn-prefix event))
              collect (make-entry :type "message" :message m)))))

(defun split-turn-request (cut event)
  "v2SplitTurnRequest as (entry-id . text), or nil."
  (when (and (cevent-split-turn event) cut)
    (let ((n (length (cevent-turn-prefix event))))
      (when (and (> n 0) (<= n (length cut)))
        (dolist (e (last cut n))
          (let ((text (history-user-text e))) (when text (return-from split-turn-request (cons (entry-id e) text))))))
      (dolist (e (reverse cut))
        (let ((text (history-user-text e))) (when text (return (cons (entry-id e) text))))))))

(defun v2-intent (entries cut request)
  "v2Intent: the branch's first user message and the cut range's last one, as (which id text)."
  (let ((out '()) (shown (make-set)))
    (when request (setf (gethash (car request) shown) t))
    (flet ((add (which e text)
             (unless (gethash (entry-id e) shown)
               (setf (gethash (entry-id e) shown) t)
               (push-end (list which (entry-id e) text) out))))
      (loop for e across entries
            do (let ((text (history-user-text e))) (when text (add "first" e text) (return))))
      (dolist (e (reverse cut))
        (let ((text (history-user-text e))) (when text (add "latest" e text) (return)))))
    out))

(defun previous-summary (entries event)
  (if (string/= (cevent-previous-summary event) "")
      (cevent-previous-summary event)
      (let ((i (find-last-compaction-index entries)))
        (if (>= i 0) (let ((s (entry-summary (aref entries i)))) (if (stringp s) s "")) ""))))

(defun resolve-summary-budget (cfg &key (context-window 0) (reserve 0) (tokens-before 0) (removed 0)
                                        (max-output 0) (headroom 0))
  "config.ResolveV2SummaryBudget."
  (let* ((removed (max 0 removed)) (budget removed))
    (when (> context-window 0)
      (let ((reserve (if (<= reserve 0) 16384 reserve)))
        (setf budget (min budget (- (+ (- context-window reserve (max 0 tokens-before)) removed) (max 0 headroom))))))
    (when (> max-output 0) (setf budget (min budget max-output)))
    (when (> (hconfig-summary-max cfg) 0) (setf budget (min budget (hconfig-summary-max cfg))))
    budget))

(defun usage-number (v)
  "ledger.UsageNumber: a finite number as an integer (truncated), else nil."
  (and (realp v) (truncate v)))

(defun run-input-usage (e)
  "ledger.runInputUsage: an assistant message's input plus cache read and cache write, or nil."
  (let ((m (entry-message e)))
    (when (and (string= (entry-type e) "message") m (equal (jget m "role") "assistant")
               (not (member (jget m "stopReason") '("aborted" "error") :test #'equal)))
      (let ((usage (jget m "usage")))
        (when (hash-table-p usage)
          (let ((input (usage-number (jget usage "input"))))
            (when input
              (+ input (or (usage-number (jget usage "cacheRead")) 0) (or (usage-number (jget usage "cacheWrite")) 0)))))))))

(defun next-run-headroom (entries)
  "ledger.NextRunHeadroom: the largest growth of the input usage between the last assistant
messages of consecutive runs since the previous compaction, a run starting at each user message."
  (let ((runs '()) (last 0) (has nil))
    (loop for i from (1+ (find-last-compaction-index entries)) below (length entries)
          do (let* ((e (aref entries i)) (m (entry-message e)))
               (when (and (string= (entry-type e) "message") m)
                 (if (equal (jget m "role") "user")
                     (progn (when has (push last runs)) (setf has nil))
                     (let ((n (run-input-usage e))) (when n (setf last n has t)))))))
    (when has (push last runs))
    (setf runs (nreverse runs))
    (loop for (a b) on runs while b maximize (- b a) into best finally (return (max 0 (or best 0))))))

;;; Level 0 inside the hook

(defun dropped-data-json (ids covers)
  "ledger.ObservationsDroppedData's encoding."
  (format nil "{\"observationIds\":~A,\"coversUpToId\":~A}"
          (json-string (coerce ids 'simple-vector)) (json-string covers)))

(defun recorded-json (observations covers)
  "ledger.ObservationsRecordedData's encoding (consolidated false, omitted)."
  (format nil "{\"observations\":[~{~A~^,~}],\"coversUpToId\":~A}" (mapcar #'obs-json observations) (json-string covers)))

(defun append-extraction-entries (h batches covers)
  "hooks.appendExtraction: appends the batches and the marker, and returns them as branch entries
with ids of their own (om-v2-extracted-<n>)."
  (let ((queue '()))
    (dolist (b batches)
      (when (and (batch-observations b) (string/= (batch-covers b) ""))
        (push-end (cons "om.observations.recorded" (recorded-json (batch-observations b) (batch-covers b))) queue)))
    (when (and (string/= covers "") (or (null batches) (string/= (batch-covers (car (last batches))) covers)))
      (push-end (cons "om.extracted" (format nil "{\"coversUpToId\":~A}" (json-string covers))) queue))
    (loop for (type . data) in queue for n from 0
          do (funcall (hhost-append h) type data)
          collect (synthetic-entry (format nil "om-v2-extracted-~D" n) type data))))

(defun host-summary (entries folded)
  "extract.HostSummary: (values record covers-up-to-id) for the latest compaction the ledger does
not back, or nil."
  (let ((index (find-last-compaction-index entries)))
    (when (< index 0) (return-from host-summary nil))
    (let ((c (aref entries index)))
      (when (or (memory-details-from (entry-details c)) (string= (entry-id c) "")) (return-from host-summary nil))
      (let ((summary (redact-secrets (one-line (if (stringp (entry-summary c)) (entry-summary c) "")))))
        (when (string= summary "") (return-from host-summary nil))
        (dolist (o (folded-observations folded))
          (when (member-str (entry-id c) (obs-source-ids o)) (return-from host-summary nil)))
        (let ((boundary (entry-index-for-id entries (entry-first-kept-id c))))
          (when (< boundary 0) (setf boundary (1+ index)))
          (when (< boundary 1) (return-from host-summary nil))
          (let ((o (make-obs :content summary :timestamp (format-timestamp (entry-time c))
                             :relevance +code-relevance+ :source-ids (list (entry-id c))
                             :token-count (estimate-tokens summary) :origin "host"
                             :source "host-summary" :authority "model" :hedge (hedge-of summary))))
            (setf (obs-id o) (hash-id (cat "host" (string (code-char 0)) (entry-id c))))
            (values o (entry-id (aref entries (1- boundary))))))))))

(defun append-host-summary (h entries folded)
  "appendHostSummaryV2: stores the host summary once and returns it as a branch entry, or nil."
  (multiple-value-bind (o covers) (host-summary entries folded)
    (when o
      (let ((data (recorded-json (list o) covers)))
        (funcall (hhost-append h) "om.observations.recorded" data)
        (synthetic-entry "om-v2-host-summary" "om.observations.recorded" data)))))

(defun extract-cut (h event)
  "extractCutV2: the branch with the cut range's extraction (and any host summary) after it."
  (let ((entries (cevent-branch event)) (fresh nil))
    (when (< (entry-index-for-id entries (cevent-first-kept event)) (compaction-range-start-index entries))
      (let* ((branch (or (ignore-errors (hhost-branch h)) entries))
             (host (append-host-summary h branch (fold-ledger branch))))
        (return-from extract-cut (if host (concatenate 'simple-vector entries (vector host)) entries))))
    (let ((branch (ignore-errors (hhost-branch h))))
      (when branch
        (let ((cut (entry-index-for-id branch (cevent-first-kept event))))
          (when (and (>= cut 0) (>= cut (compaction-range-start-index branch))) (setf entries branch fresh t)))))
    (let* ((range-start (compaction-range-start-index entries))
           (cut (entry-index-for-id entries (cevent-first-kept event)))
           (start (extraction-start-index entries))
           (folded (fold-ledger entries))
           (appended
             (multiple-value-bind (batches covers)
                 (extract entries start cut folded :skip-tools (hhost-skip-tools h) :cwd (hhost-cwd h)
                                                   :file-ops (cevent-file-ops event) :file-ops-from range-start)
               (let ((record nil))
                 (when (hhost-detect h)
                   (multiple-value-setq (batches record) (funcall (hhost-detect h) entries start cut folded batches covers)))
                 (stamp-batches (hhost-structure h) batches)
                 (let ((out (append-extraction-entries h batches covers)))
                   ;; Counted only for an extraction that landed on a range measured on the fresh
                   ;; branch, as extractCutV2 records it.
                   (when (and out fresh record) (funcall record))
                   out))))
           (host (append-host-summary h entries folded)))
      (when host (setf appended (append appended (list host))))
      (if appended (concatenate 'simple-vector entries appended) entries))))

;;; Level 1's report

(defun level1-skip-reason (cfg event entries cut &key model own-trigger)
  "consolidateV2's reasons for making no request, in its order. With no session model the
request is never made (om hands none); a model is not ported (the server refuses it first).
own-trigger is runtime.CompactInFlight: the extension's own compaction."
  (cond ((hconfig-consolidate-off cfg) "consolidate is false")
        ((hconfig-passive cfg) "passive mode")
        (own-trigger "the extension's own compaction (PiG's auto-compaction is off) runs level 0 only")
        ((string= (cevent-reason event) "overflow") "an overflow compaction runs level 0 only, so recovery is not delayed")
        ((below-raw-floor-p (hconfig-raw-floor cfg) (hconfig-limits cfg) (hconfig-model-limits cfg) model (cevent-tokens-before event))
         "the context is below the session model's raw floor: nothing is consolidated")
        ((or (null cut) (< (entry-index-for-id entries (cevent-first-kept event)) 0)) "the cut range is not on the branch")
        ((null model) "the session model is unavailable: no session model")
        (t (error "level 1 with a session model is not ported"))))

(defun consolidation-json (reason)
  (format nil "{\"requested\":false,\"applied\":false~@[,\"reason\":~A~]}"
          (and (string/= reason "") (json-string reason))))

;;; Details

(defun alias-json (a)
  "ledger.SubjectAlias's encoding: from, to, then reason when set."
  (format nil "{\"from\":~A,\"to\":~A~@[,\"reason\":~A~]}" (json-string (subject-alias-from a))
          (json-string (subject-alias-to a))
          (and (string/= (subject-alias-reason a) "") (json-string (subject-alias-reason a)))))

(defun details-json (observations reflections aliases first-kept consolidation shown &optional summary-codec)
  "v2Details' om.folded details with the report and the shown ids, in MemoryDetails' order;
summary-codec the summary's codec when it is not rows."
  (with-output-to-string (out)
    (format out "{\"type\":\"om.folded\",\"version\":1,\"fullFold\":true,\"observations\":[~{~A~^,~}],\"reflections\":[~{~A~^,~}]"
            (mapcar #'obs-json observations) (mapcar #'refl-json reflections))
    (when (string/= first-kept "") (format out ",\"foldThroughEntryId\":~A" (json-string first-kept)))
    (when summary-codec (format out ",\"summaryCodec\":~A" (json-string summary-codec)))
    (when aliases (format out ",\"aliases\":[~{~A~^,~}]" (mapcar #'alias-json aliases)))
    (write-string ",\"engine\":\"v2\"" out)
    (when consolidation (format out ",\"consolidation\":~A" consolidation))
    (when shown (format out ",\"shown\":~A" (json-string (coerce shown 'simple-vector))))
    (write-string "}" out)))

;;; The hook

;; The session model's profile (src/harness/profile.lisp), bound by the PiG compaction: nil is
;; harness.Default (the whole room, tables, rows). profile-budget, profile-format, profile-codec
;; and profile-summary-codec read it.
(defvar *summary-profile* nil)

;; Whether the kernel floor of the store the event reads is lowered (src/harness/floor.lisp): the Go
;; layer's kernel check (src/layer/go-layer.lisp) then admits every definition, as kernel.Floor.Check.
(defvar *kernel-floor-lowered* nil)

;;; The sleep pass's compaction fallback (hooks/sleep_v2.go)

(defun sleep-structure (h entries)
  "SleepInput's repository and view answer: (values repo answer), nil without a view."
  (let ((repo (encoding-repo)))
    (when (and (hhost-structure h) repo)
      (let ((q (project-query (folded-active (fold-ledger entries)) repo)))
        (unless (project-query-empty-p q)
          (let ((answer (funcall (hhost-structure h) q)))
            (when answer (values repo answer))))))))

(defun run-sleep (h cfg entries trigger)
  "RunSleep: one pass over entries appended through the host; the record's branch entry, nil when
the branch is empty or its tip is a sleep record."
  (let ((at (sleep-indexes entries)))
    (when (or (zerop (length entries)) (and at (= (car (last at)) (1- (length entries)))))
      (return-from run-sleep nil)))
  (multiple-value-bind (repo answer) (sleep-structure h entries)
    (let ((data (sleep-pass entries trigger :decay (hconfig-decay cfg) :decay-horizon (hconfig-decay-horizon cfg)
                                            :repo repo :structure answer)))
      (when data
        (funcall (hhost-append h) "om.sleep" data)
        (synthetic-entry "om-v2-sleep" "om.sleep" data)))))

(defun sleep-fallback (h cfg entries)
  "sleepFallbackV2: the pass inside a compaction once sleepAfterCompactions compactions passed with
no sleep record and the ledger is within sleepMaxRecords; entries with its record."
  (let ((k (hconfig-sleep-after cfg)))
    (if (or (<= k 0) (< (compactions-since-sleep entries) k)
            (and (plusp (hconfig-sleep-max-records cfg))
                 (> (length (folded-active (fold-ledger entries))) (hconfig-sleep-max-records cfg))))
        entries
        (let ((e (run-sleep h cfg entries "compaction")))
          (if e (concatenate 'simple-vector entries (vector e)) entries)))))

(defun build-compaction (h event cfg &key model own-trigger (context-window 0) (max-output 0) level1)
  "buildCompactionV2: a cresult on the event's exact cut, then the branch it folded (level 0's
batches and level 1's appends after it) and the cut range's source entries. context-window and
max-output are the session model's (host.ModelContextWindow, ModelMaxTokens; 0 unknown).
level1, when given, is consolidateV2 (server/level1.lisp): a function of (entries cut
memory-budget) returning (values appended-entries applied-p report-json usage); without it level 1
is skipped with level1-skip-reason's report."
  (let* ((cut (source-entries (cut-range (cevent-branch event) event)))
         (entries (sleep-fallback h cfg (extract-cut h event)))
         (previous (previous-summary entries event))
         (removed (+ (source-tokens cut) (estimate-tokens previous)))
         (budget (profile-budget
                  (resolve-summary-budget cfg :reserve (cevent-reserve event) :tokens-before (cevent-tokens-before event)
                                              :removed removed :context-window context-window :max-output max-output
                                              :headroom (next-run-headroom entries))))
         (fmt (profile-format (hconfig-memory-format cfg)))
         (instructions (trim-space (cevent-custom-instructions event)))
         (iblock (if (string/= instructions "") (cat (string #\Newline) (string #\Newline) "---" (string #\Newline) (string #\Newline) instructions) ""))
         (memory-budget (if (and (string/= iblock "") (> (- budget (estimate-tokens iblock)) 0))
                            (- budget (estimate-tokens iblock))
                            budget))
         ;; Level 1: at most one request; its appends are folded through PiG's cut, and when it
         ;; applied, the instructions shaped it and the memory gets their room.
         ;; The project records' freshness against HEAD (decision 20), before level 1.
         (verdicts (compaction-freshness h entries))
         (level1-result (and level1 (let ((*freshness* verdicts))
                                      (multiple-value-list (funcall level1 entries cut memory-budget)))))
         (entries (if (first level1-result) (concatenate 'simple-vector entries (first level1-result)) entries))
         (iblock (if (second level1-result) "" iblock))
         (memory-budget (if (second level1-result) budget memory-budget))
         (consolidation (if level1
                            (third level1-result)
                            (consolidation-json (level1-skip-reason cfg event entries cut :model model :own-trigger own-trigger))))
         (projection (full-projection* entries (cevent-first-kept event)))
         (observations (projection-observations projection))
         (reflections (active-reflections (projection-reflections projection)))
         (summary "") (kind "") (shown '()))
    (when (and (> memory-budget 0) (or observations reflections))
      (let* ((folded (fold-ledger entries))
             (request (split-turn-request cut event))
             (r (render-summary observations folded entries memory-budget
                                :reflections reflections :request request :intent (v2-intent entries cut request)
                                :format fmt :codec (profile-codec fmt) :aliases (projection-aliases projection)
                                :reflection-dates (reflection-date-ranges (folded-reflections folded) (folded-by-id folded))
                                :slots (subseq (limits-for (hconfig-limits cfg) (hconfig-model-limits cfg) model) 0 3)
                                :freshness (project-texts verdicts)
                                :hidden (sleep-hidden entries)
                                :decayed (and (string/= (hconfig-decay cfg) "")
                                              (decayed-records entries observations (hconfig-decay cfg)
                                                               (if (plusp (hconfig-decay-horizon cfg)) (hconfig-decay-horizon cfg) +default-decay-horizon+))))))
        (when (and (string/= (summary-text r) "") (<= (estimate-tokens (summary-text r)) memory-budget))
          (setf summary (summary-text r) kind "records"
                shown (append (mapcar #'refl-id (summary-reflections r)) (mapcar #'obs-id (summary-observations r))))
          (let ((dropped (drops observations (summary-observations r) (folded-by-id folded) (summary-reflections r))))
            (setf observations (remove-if (lambda (o) (member-str (obs-id o) dropped)) observations))
            (when (and cut dropped)
              (funcall (hhost-append h) "om.observations.dropped" (dropped-data-json dropped (entry-id (car (last cut))))))))))
    (when (string= summary "")
      (multiple-value-setq (summary kind) (minimal-brief cut (hhost-skip-tools h) memory-budget removed)))
    (when (and (string/= iblock "") (<= (estimate-tokens (cat summary iblock)) budget))
      (setf summary (cat summary iblock)))
    (values (make-cresult :summary summary :first-kept (cevent-first-kept event) :tokens-before (cevent-tokens-before event)
                          :kind kind
                          :usage (fourth level1-result)
                          :details-json (details-json observations reflections (projection-aliases projection)
                                                      (cevent-first-kept event) consolidation shown
                                                      (and (string= kind "records") (profile-summary-codec fmt))))
            entries cut)))

(defun cresult-map-json (c)
  "CompactionResult.ToMap's encoding: its keys sorted, details in MemoryDetails' order."
  (format nil "{\"details\":~A,\"firstKeptEntryId\":~A,\"summary\":~A,\"tokensBefore\":~D~@[,\"usage\":~A~]}"
          (cresult-details-json c) (json-string (cresult-first-kept c)) (json-string (cresult-summary c))
          (cresult-tokens-before c) (and (cresult-usage c) (json-string (cresult-usage c)))))

;;; neutral's compact.go

(defun neutral-tokens-before (branch cut)
  "neutral.tokensBefore: the estimated tokens of the entries from the range start up to cut."
  (let ((end (entry-index-for-id branch cut)) (start (compaction-range-start-index branch)))
    (when (< end 0) (setf end (length branch)))
    (if (< end start) 0 (loop for i from start below end sum (estimate-entry-tokens (aref branch i))))))

(defun compact-on (h cfg reason cut before)
  "neutral.Host.compact: the compaction on cut, tokensBefore estimated when before is 0."
  (let ((branch (hhost-branch h)))
    (when (<= before 0) (setf before (neutral-tokens-before branch cut)))
    (build-compaction h (make-cevent :first-kept cut :tokens-before before :branch branch :reason reason) cfg)))

(defun latest-summary (branch)
  "neutral.Host.LatestSummary over a branch."
  (let ((i (find-last-compaction-index branch)))
    (if (< i 0) "" (let ((s (entry-summary (aref branch i)))) (if (stringp s) s "")))))
