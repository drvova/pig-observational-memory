;;;; Memory v2's kernel detectors over one level-0 extraction (memory/rules detect.go, v2.go
;;;; DetectV2 and RecordDetection; remedy.go KeepHead) and the hook's use of them
;;;; (memory/hooks/harness_v2.go detectV2 and placeNotesV2): uncited-assistant-specifics and
;;;; dropped-then-queried are counted over what the extraction read, the keep-head rule's notes
;;;; ride in the batches of their cut-point segments, and the run is recorded in the model's usage
;;;; ledger once the extraction is appended.
;;;;
;;;; A memory call written in the removed query language (a memory tool call with a query and no
;;;; code, or a call of a retired defined tool) is read with the frozen query parser
;;;; (src/legacy/legacyquery.lisp, memory-asks in src/legacy/notes.lisp). legacy-memory-call is
;;;; kept for callers that still name it; nothing signals it now.

(in-package "OM.LAYER")

(define-condition legacy-memory-call (error) ((entry :initarg :entry :reader legacy-memory-call-entry))
  (:report (lambda (c s) (format s "a memory call in the removed query language (entry ~A)" (legacy-memory-call-entry c)))))

(defparameter +keep-head-chars+ 400 "rules.KeepHeadChars, om.HistoryClipChars.")

(defun msg-of (e) (and (string= (om::entry-type e) "message") (om::entry-message e)))

(defun role-p (e role) (let ((m (msg-of e))) (and m (equal (om::jget m "role") role))))

(defun block-text (content)
  "blockText: a string, or the text blocks joined by newlines."
  (cond ((stringp content) content)
        ((and (vectorp content) (not (stringp content)))
         (om::join (loop for b across content
                         when (and (hash-table-p b) (equal (om::jget b "type") "text") (stringp (om::jget b "text")))
                           collect (om::jget b "text"))
                   (string #\Newline)))
        (t "")))

(defun assistant-text (e)
  "AssistantText: (values text ok)."
  (if (role-p e "assistant") (values (block-text (om::jget (msg-of e) "content")) t) (values "" nil)))

(defun ends-turn-p (e)
  (cond ((not (role-p e "assistant")) nil)
        ((equal (om::jget (msg-of e) "stopReason") "toolUse") nil)
        ((string/= (om::trim-space (block-text (om::jget (msg-of e) "content"))) "") t)
        (t (let ((blocks (om::jget (msg-of e) "content")))
             (not (and (vectorp blocks) (not (stringp blocks))
                       (some (lambda (b) (and (hash-table-p b) (equal (om::jget b "type") "toolCall"))) blocks)))))))

(defun memory-result-p (e defined)
  (and (role-p e "toolResult")
       (let ((name (om::jget (msg-of e) "toolName")))
         (or (equal name "memory") (and (stringp name) (string/= name "") (assoc name defined :test #'string=) t)))))

(defun turn-carriers (history at defined)
  (loop for i from (1- at) downto 0
        until (ends-turn-p (aref history i))
        when (or (role-p (aref history i) "user") (memory-result-p (aref history i) defined))
          collect (block-text (om::jget (msg-of (aref history i)) "content"))))

(defun uncited-assistant-specifics (branch covered notes known defined)
  "UncitedAssistantSpecifics: the covered assistant entry ids, in order, no note of the pass
cites whose text adds specifics its turn and memory did not give. branch and covered are vectors."
  (let ((cited (make-hash-table :test 'equal)) (memory nil)
        (branch-index (om::entry-index-by-id branch)) (covered-index (om::entry-index-by-id covered))
        (out '()))
    (dolist (o notes) (dolist (id (om::obs-source-ids o)) (setf (gethash id cited) t)))
    (loop for e across covered
          do (multiple-value-bind (text ok) (assistant-text e)
               (when (and ok (not (gethash (om::entry-id e) cited)) (om::has-specifics-p text))
                 (multiple-value-bind (i found) (gethash (om::entry-id e) branch-index)
                   (let ((history (if found branch covered)) (at (if found i (gethash (om::entry-id e) covered-index))))
                     (unless memory
                       (setf memory (om::new-given (append (mapcar #'om::obs-content known) (mapcar #'om::obs-content notes)))))
                     (when (om::added text (list (om::new-given (turn-carriers history at defined)) memory))
                       (push (om::entry-id e) out)))))))
    (nreverse out)))

(defun memory-results (branch)
  "memoryResults: the memory tool results' details by tool call id."
  (let ((out (make-hash-table :test 'equal)))
    (loop for e across branch
          when (and (role-p e "toolResult") (equal (om::jget (msg-of e) "toolName") "memory"))
            do (let ((id (om::jget (msg-of e) "toolCallId")))
                 (when (and (stringp id) (string/= id "")) (setf (gethash id out) (om::jget (msg-of e) "details")))))
    out))

(defun code-details-asks (d)
  "omtool.DetailsFrom's recalled and searches: (values recalled searches ok)."
  (flet ((strings (v) (cond ((or (null v) (eq v :null)) (values '() t))
                            ((and (vectorp v) (not (stringp v)) (every (lambda (x) (or (stringp x) (eq x :null))) v))
                             (values (loop for x across v collect (if (stringp x) x "")) t))
                            (t (values nil nil)))))
    (if (and (hash-table-p d) (nth-value 1 (gethash "code" d)) (nth-value 1 (gethash "outcome" d)))
        (multiple-value-bind (recalled ok1) (strings (om::jget d "recalled"))
          (multiple-value-bind (searches ok2) (strings (om::jget d "searches"))
            (if (and ok1 ok2) (values recalled searches t) (values nil nil nil))))
        (values nil nil nil))))

;; memory-asks (rules.MemoryAsks) is src/legacy/notes.lisp's: it reads old calls with the frozen
;; query parser (legacyquery.lisp). defined is harness.DefinedQueries, an alist (name . query).

(defun dropped-then-queried (branch covered defined)
  "DroppedThenQueried: the dropped records the memory calls in covered asked for."
  (let ((in-covered (make-hash-table :test 'equal)) (results (memory-results branch))
        (records (make-hash-table :test 'equal)) (dropped-at (make-hash-table :test 'equal)) (out '()))
    (loop for e across covered do (setf (gethash (om::entry-id e) in-covered) t))
    (loop for e across branch for i from 0
          do (dolist (o (om::recorded-data e)) (setf (gethash (om::obs-id o) records) o))
             (when (string= (om::entry-type e) "compaction")
               (let ((d (om::memory-details-from (om::entry-details e))))
                 (when d
                   (dolist (o (om::details-observations d))
                     (unless (nth-value 1 (gethash (om::obs-id o) records)) (setf (gethash (om::obs-id o) records) o))))))
             (dolist (id (om::dropped-data e))
               (unless (nth-value 1 (gethash id dropped-at)) (setf (gethash id dropped-at) i)))
             (when (gethash (om::entry-id e) in-covered)
               (dolist (ask (memory-asks e defined results))
                 (let ((asked (make-hash-table :test 'equal)))
                   (dolist (id (car ask)) (when (nth-value 1 (gethash id dropped-at)) (setf (gethash id asked) t)))
                   (dolist (search (cdr ask))
                     (let ((words (om::fields (om::to-lower search))))
                       (loop for id being the hash-keys of dropped-at
                             do (let ((o (gethash id records)))
                                  (when (and o words (every (lambda (w) (search w (om::to-lower (om::obs-content o)))) words))
                                    (setf (gethash id asked) t))))))
                   (let ((ids (sort (loop for id being the hash-keys of asked
                                          when (gethash id records) collect (om::obs-id (gethash id records)))
                                    #'string<)))
                     (dolist (id ids) (push (gethash id records) out)))))))
    (nreverse out)))

(defstruct findings (counts '()) (uncited '()) (dropped-kinds '()))

(defun alist-incf (alist key n)
  (let ((cell (assoc key alist :test #'string=)))
    (if cell (progn (incf (cdr cell) n) alist) (acons key n alist))))

(defun detect-v2 (branch covered notes known defined)
  "rules.DetectV2: the findings of one extraction (covered its source entries, a vector; notes
its records; known the memory before it)."
  (let* ((uncited (uncited-assistant-specifics branch covered notes known defined))
         (asked (dropped-then-queried branch covered defined))
         (kinds '()))
    (dolist (o asked)
      (setf kinds (alist-incf kinds (if (string= (om::obs-kind o) "") +unclassified+ (om::obs-kind o)) 1)))
    (make-findings :counts (list (cons +detect-uncited+ (length uncited)) (cons +detect-dropped-queried+ (length asked)))
                   :uncited uncited :dropped-kinds kinds)))

(defun source-tokens (entries)
  "rules.SourceTokens."
  (loop for e across entries sum (om::estimate-entry-tokens e)))

;;; keep-head (remedy.go KeepHead, opening)

(defun opening (text)
  (let ((taken '()))
    (dolist (p (om::split (om::replace-all text (coerce '(#\Return #\Newline) 'string) (string #\Newline))
                          (coerce '(#\Newline #\Newline) 'string)))
      (let ((p (om::join (om::fields p) " ")))
        (when (string/= p "")
          (push-end-list p taken)
          (when (om::has-specifics-p p) (return)))))
    (let ((head (om::join taken " ")))
      (if (> (length head) +keep-head-chars+)
          (concatenate 'string (om::trim-space (subseq head 0 +keep-head-chars+)) "…")
          head))))

(defun now-millis () (floor (time-nanos (now)) 1000000))

(defun keep-head (branch flagged chunk-order covers existing)
  "rules.KeepHead: (values kept refused)."
  (let ((boundary -1) (position (make-hash-table :test 'equal)) (by-id (om::entry-index-by-id branch))
        (seen (make-hash-table :test 'equal)) (kept '()) (refused '()))
    (loop for id in chunk-order for i from 0
          do (setf (gethash id position) i) (when (string= id covers) (setf boundary i)))
    (dolist (o existing) (setf (gethash (om::obs-id o) seen) t))
    (dolist (id flagged)
      (multiple-value-bind (pos in-chunk) (gethash id position)
        (if (or (not in-chunk) (> pos boundary))
            (push-end-list id refused)
            (multiple-value-bind (index ok) (gethash id by-id)
              (when ok
                (multiple-value-bind (text ok) (assistant-text (aref branch index))
                  (let ((head (and ok (opening text))))
                    (when (and head (string/= head ""))
                      (let* ((content (om::truncate-record-content (om::redact-secrets (concatenate 'string "Assistant said: " head))))
                             (obs-id (om::hash-id content)))
                        (unless (gethash obs-id seen)
                          (setf (gethash obs-id seen) t)
                          (let ((stamp (om::format-timestamp (om::entry-timestamp (aref branch index)))))
                            (when (om::has-prefix stamp "?") (setf stamp (om::format-millis (now-millis))))
                            (push-end-list (om::make-obs :id obs-id :content content :timestamp stamp :relevance "low"
                                                         :kind "assistant" :source-ids (list id)
                                                         :token-count (om::observation-line-tokens obs-id stamp "low" content))
                                           kept))))))))))))
    (values kept refused)))

(defun place-notes (batches notes entries last)
  "placeNotesV2: each note in the batch of its source entry's cut-point segment."
  (if (null notes)
      batches
      (let ((index (om::entry-index-by-id entries))
            (batches (mapcar (lambda (b) (om::make-batch :observations (copy-list (om::batch-observations b))
                                                         :covers (om::batch-covers b)))
                             batches)))
        (dolist (note notes)
          (when (om::obs-source-ids note)
            (multiple-value-bind (at ok) (gethash (first (om::obs-source-ids note)) index)
              (when (and ok (<= at last))
                (let* ((cover (om::entry-id (aref entries (om::segment-end entries at last))))
                       (b (find cover batches :key #'om::batch-covers :test #'string=)))
                  (if b
                      (push-end-list note (om::batch-observations b))
                      (push-end-list (om::make-batch :observations (list note) :covers cover) batches)))))))
        (stable-sort batches #'< :key (lambda (b) (gethash (om::batch-covers b) index -1))))))

;;; The hook's detector run (harness_v2.go detectV2)

(defun harness-detect (store model entries start end folded batches covers &key (defined '()))
  "detectV2 over one level-0 extraction of entries[start:end] (batches and covers, before they
are appended) for model: (values batches record), the batches with the keep-head rule's notes
placed, and record, a function that records the run in the model's usage ledger, to be called
only once the extraction landed. Without a store or a model, or when the extraction read
nothing, it changes nothing and record records nothing. Signals legacy-memory-call."
  (when (or (null store) (string= store "") (null model) (string= model "") (string= covers ""))
    (return-from harness-detect (values batches (lambda () nil))))
  (let ((covered '()) (read '()) (last -1))
    (loop for i from (max start 0) below (min end (length entries))
          for e = (aref entries i)
          when (om::source-entry-p e)
            do (push e covered) (push (om::entry-id e) read) (setf last i)
               (when (string= (om::entry-id e) covers) (return)))
    (let* ((covered (coerce (nreverse covered) 'simple-vector)) (read (nreverse read))
           (notes (loop for b in batches append (om::batch-observations b)))
           (findings (detect-v2 entries covered notes (om::folded-active folded) defined))
           (tokens (source-tokens covered)))
      (when (and notes (rules-have-p (active-rules store model) +remedy-keep-head+))
        (let ((existing (append (om::folded-observations folded) notes)))
          (multiple-value-bind (kept) (keep-head entries (findings-uncited findings) read covers existing)
            (dolist (o kept) (setf (om::obs-origin o) "code"))
            (setf batches (place-notes batches kept entries last)))))
      (values batches (lambda () (record-detection store model findings tokens))))))
