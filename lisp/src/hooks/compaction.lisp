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
  (detect nil))

(defstruct hconfig
  "The settings the hook reads (config.Config): passive, consolidate, compactionSummaryMaxTokens,
memoryFormat."
  (passive nil) (consolidate-off nil) (summary-max 0) (memory-format "tables"))

(defstruct cevent
  "hooks.BeforeCompactEvent with its preparation's members."
  (first-kept "") (tokens-before 0) branch (reason "") (custom-instructions "")
  (messages-to-summarize '()) (turn-prefix '()) (split-turn nil) (previous-summary "")
  (file-ops '()) (reserve 0))

(defstruct cresult summary (first-kept "") (tokens-before 0) (details-json "") (kind ""))

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
      (let ((summary (one-line (if (stringp (entry-summary c)) (entry-summary c) ""))))
        (when (string= summary "") (return-from host-summary nil))
        (dolist (o (folded-observations folded))
          (when (member-str (entry-id c) (obs-source-ids o)) (return-from host-summary nil)))
        (let ((boundary (entry-index-for-id entries (entry-first-kept-id c))))
          (when (< boundary 0) (setf boundary (1+ index)))
          (when (< boundary 1) (return-from host-summary nil))
          (let ((o (make-obs :content summary :timestamp (format-timestamp (entry-time c))
                             :relevance +code-relevance+ :source-ids (list (entry-id c))
                             :token-count (estimate-tokens summary) :origin "host")))
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

(defun details-json (observations reflections aliases first-kept consolidation shown)
  "v2Details' om.folded details with the report and the shown ids, in MemoryDetails' order."
  (with-output-to-string (out)
    (format out "{\"type\":\"om.folded\",\"version\":1,\"fullFold\":true,\"observations\":[~{~A~^,~}],\"reflections\":[~{~A~^,~}]"
            (mapcar #'obs-json observations) (mapcar #'refl-json reflections))
    (when (string/= first-kept "") (format out ",\"foldThroughEntryId\":~A" (json-string first-kept)))
    (when aliases (format out ",\"aliases\":[~{~A~^,~}]" (mapcar #'alias-json aliases)))
    (write-string ",\"engine\":\"v2\"" out)
    (when consolidation (format out ",\"consolidation\":~A" consolidation))
    (when shown (format out ",\"shown\":~A" (json-string (coerce shown 'simple-vector))))
    (write-string "}" out)))

;;; The hook

(defun build-compaction (h event cfg &key model own-trigger (context-window 0) (max-output 0))
  "buildCompactionV2 at level 0: a cresult on the event's exact cut, then the branch it folded
(level 0's batches after it) and the cut range's source entries. context-window and max-output
are the session model's (host.ModelContextWindow, ModelMaxTokens; 0 unknown)."
  (let* ((cut (source-entries (cut-range (cevent-branch event) event)))
         (entries (extract-cut h event))
         (previous (previous-summary entries event))
         (removed (+ (source-tokens cut) (estimate-tokens previous)))
         (budget (resolve-summary-budget cfg :reserve (cevent-reserve event) :tokens-before (cevent-tokens-before event)
                                             :removed removed :context-window context-window :max-output max-output
                                             :headroom (next-run-headroom entries)))
         (fmt (if (string= (hconfig-memory-format cfg) "lines") "lines" "tables"))
         (instructions (trim-space (cevent-custom-instructions event)))
         (iblock (if (string/= instructions "") (cat (string #\Newline) (string #\Newline) "---" (string #\Newline) (string #\Newline) instructions) ""))
         (memory-budget (if (and (string/= iblock "") (> (- budget (estimate-tokens iblock)) 0))
                            (- budget (estimate-tokens iblock))
                            budget))
         (reason (level1-skip-reason cfg event entries cut :model model :own-trigger own-trigger))
         (projection (full-projection* entries (cevent-first-kept event)))
         (observations (projection-observations projection))
         (reflections (active-reflections (projection-reflections projection)))
         (summary "") (kind "") (shown '()))
    (when (and (> memory-budget 0) (or observations reflections))
      (let* ((folded (fold-ledger entries))
             (request (split-turn-request cut event))
             (r (render-summary observations folded entries memory-budget
                                :reflections reflections :request request :intent (v2-intent entries cut request)
                                :format fmt :codec :rows :aliases (projection-aliases projection)
                                :reflection-dates (reflection-date-ranges (folded-reflections folded) (folded-by-id folded)))))
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
                          :details-json (details-json observations reflections (projection-aliases projection)
                                                      (cevent-first-kept event) (consolidation-json reason) shown))
            entries cut)))

(defun cresult-map-json (c)
  "CompactionResult.ToMap's encoding: its keys sorted, details in MemoryDetails' order."
  (format nil "{\"details\":~A,\"firstKeptEntryId\":~A,\"summary\":~A,\"tokensBefore\":~D}"
          (cresult-details-json c) (json-string (cresult-first-kept c)) (json-string (cresult-summary c))
          (cresult-tokens-before c)))

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
