;;;; memory/ledger fold.go, projection.go and progress.go: the om.* entries' payloads (types.go),
;;;; the fold of the branch's ledger with level 1's replacements, the projections a summary and a
;;;; compaction read, compaction details (om.folded), and where level 0 starts reading.

(in-package "OM.KERNEL")

;;; Payloads

(defun custom-of-p (e custom-type)
  (and (string= (entry-type e) "custom") (string= (entry-custom-type e) custom-type)))

(defun non-empty-string-p (v) (and (stringp v) (string/= v "")))

(defun json-array-p (v) (and (vectorp v) (not (stringp v))))

(defun observation-list (v require-non-empty)
  "observationList: (values records ok)."
  (if (or (not (json-array-p v)) (and require-non-empty (zerop (length v))))
      (values nil nil)
      (let ((out (map 'list #'observation-from v)))
        (if (member nil out) (values nil nil) (values out t)))))

(defun reflection-list (v require-non-empty)
  (if (or (not (json-array-p v)) (and require-non-empty (zerop (length v))))
      (values nil nil)
      (let ((out (map 'list #'reflection-from v)))
        (if (member nil out) (values nil nil) (values out t)))))

(defun recorded-data (e)
  "ObservationsRecordedEntry: (values observations covers-up-to-id consolidated), or nil."
  (when (and (custom-of-p e "om.observations.recorded") (hash-table-p (entry-data e)))
    (let ((data (entry-data e)))
      (multiple-value-bind (observations ok) (observation-list (jget data "observations") t)
        (when (and ok (non-empty-string-p (jget data "coversUpToId")))
          (values observations (jget data "coversUpToId") (jtrue-p (jget data "consolidated"))))))))

(defun reflections-data (e)
  "ReflectionsRecordedEntry: (values reflections covers-up-to-id), or nil."
  (when (and (custom-of-p e "om.reflections.recorded") (hash-table-p (entry-data e)))
    (let ((data (entry-data e)))
      (multiple-value-bind (reflections ok) (reflection-list (jget data "reflections") t)
        (when (and ok (non-empty-string-p (jget data "coversUpToId")))
          (values reflections (jget data "coversUpToId")))))))

(defun dropped-data (e)
  "ObservationsDroppedEntry: (values ids covers-up-to-id consolidated), or nil."
  (when (and (custom-of-p e "om.observations.dropped") (hash-table-p (entry-data e)))
    (let* ((data (entry-data e)) (ids (non-empty-strings (jget data "observationIds"))) (covers (jget data "coversUpToId")))
      (when (and ids (non-empty-string-p covers))
        (values ids covers (jtrue-p (jget data "consolidated")))))))

(defun extracted-data (e)
  "ExtractedEntry: the covers-up-to id, or nil."
  (when (and (custom-of-p e "om.extracted") (hash-table-p (entry-data e)))
    (let ((c (jget (entry-data e) "coversUpToId"))) (and (non-empty-string-p c) c))))

;;; The fold

(defstruct folded
  (observations '())
  (active '())
  (dropped (make-set))
  (by-id (make-set))
  (reflections '())
  (refl-by-id (make-set))
  (aliases '())
  (cited-by (make-set))
  (replaced-by (make-set))
  ;; truth: what truth maintenance took out (ledger-truth.lisp, FoldedLedger.Truth).
  (truth (make-truth))
  ;; challenged: each record a sleep check read, to t when its latest verdict challenged it
  ;; (FoldedLedger.Challenged; sleep.lisp).
  (challenged (make-set)))

;;; om.sleep (ledger/sleep.go SleepEntry): version 1, a trigger and a through id.

(defun sleep-data (e)
  "SleepEntry: the data of a valid om.sleep entry, else nil."
  (when (and (string= (entry-type e) "custom") (equal (entry-custom-type e) "om.sleep"))
    (let ((d (entry-data e)))
      (and (hash-table-p d)
           (let ((v (jget d "version"))) (and (realp v) (= v 1)))
           (non-empty-string-p (jget d "trigger"))
           (non-empty-string-p (jget d "throughEntryId"))
           (realp (jget d "records"))
           d))))

(defun sleep-items (d key)
  "The objects of a sleep record's list key, in order."
  (let ((v (jget d key)))
    (if (and (vectorp v) (not (stringp v))) (remove-if-not #'hash-table-p (coerce v 'list)) '())))

(defun fold-end-index (entries up-to)
  (if (string= up-to "")
      (1- (length entries))
      (or (position up-to entries :key #'entry-id :test #'string=) (1- (length entries)))))

(defun folded-derive (f)
  (setf (folded-active f) (remove-if (lambda (o) (gethash (obs-id o) (folded-dropped f))) (folded-observations f)))
  (let ((cited (make-set)))
    (dolist (r (folded-reflections f))
      (dolist (id (refl-supporting r))
        (unless (member-str (refl-id r) (gethash id cited))
          (push-end (refl-id r) (gethash id cited)))))
    (setf (folded-cited-by f) cited))
  f)

(defun fold-ledger-unresolved (entries &optional (up-to ""))
  "FoldLedgerUnresolved: the ledger through up-to (\"\" is the tip), links as written."
  (let* ((f (make-folded)) (observations '()) (reflections '())
         (tracker (make-tracker (lambda (id) (gethash id (folded-by-id f)))
                                (lambda (id) (hidden-by-p id observations (folded-dropped f) reflections))))
         (end (fold-end-index entries up-to)))
    (loop for i from 0 to end
          do (let ((e (aref entries i)))
              (when (string= (entry-type e) "custom")
               (let ((type (entry-custom-type e)))
                 (cond ((string= type "om.observations.recorded")
                        (multiple-value-bind (list covers consolidated) (recorded-data e)
                          (when covers
                            (dolist (o list)
                              (unless (nth-value 1 (gethash (obs-id o) (folded-by-id f)))
                                (setf (gethash (obs-id o) (folded-by-id f)) o)
                                (push o observations)))
                            (tracker-recorded tracker list covers consolidated))))
                       ((string= type "om.reflections.recorded")
                        (multiple-value-bind (list covers) (reflections-data e)
                          (when covers
                            (dolist (r list)
                              (unless (nth-value 1 (gethash (refl-id r) (folded-refl-by-id f)))
                                (setf (gethash (refl-id r) (folded-refl-by-id f)) r)
                                (push r reflections))))))
                       ((string= type "om.observations.dropped")
                        (multiple-value-bind (ids covers consolidated) (dropped-data e)
                          (when covers
                            (dolist (id ids) (setf (gethash id (folded-dropped f)) t))
                            (tracker-dropped tracker ids covers consolidated))))
                       ((string= type "om.subjects.aliased")
                        (let ((aliases (subjects-aliased-data e)))
                          (when aliases (setf (folded-aliases f) (append (folded-aliases f) aliases)))))
                       ((string= type "om.sleep")
                        (let ((d (sleep-data e)))
                          (when d
                            (dolist (c (sleep-items d "checks"))
                              (when (stringp (jget c "id"))
                                (setf (gethash (jget c "id") (folded-challenged f)) (equal (jget c "verdict") "challenged"))))))))))))
    (multiple-value-bind (kept kept-refl truth)
        (maintain-truth (nreverse observations) (folded-dropped f) (tracker-by tracker) (nreverse reflections))
      (setf (folded-observations f) kept
            (folded-reflections f) kept-refl
            (folded-truth f) truth))
    (folded-keep-by-id f)
    (setf (folded-replaced-by f) (tracker-by tracker))
    (folded-derive f)))

(defun folded-keep-by-id (f)
  "FoldedLedger.keepByID: the by-id maps hold only the records and reflections the lists hold."
  (unless (= (hash-table-count (folded-by-id f)) (length (folded-observations f)))
    (let ((m (make-set)))
      (dolist (o (folded-observations f)) (setf (gethash (obs-id o) m) o))
      (setf (folded-by-id f) m)))
  (unless (= (hash-table-count (folded-refl-by-id f)) (length (folded-reflections f)))
    (let ((m (make-set)))
      (dolist (r (folded-reflections f)) (setf (gethash (refl-id r) m) r))
      (setf (folded-refl-by-id f) m))))

(defun resolve-folded (f &optional follow)
  "ResolveFolded: every link of f read through its replacements."
  (when (plusp (hash-table-count (folded-replaced-by f)))
    (let ((by (folded-replaced-by f)))
      (setf (folded-observations f)
            (mapcar (lambda (o) (let ((r (resolve-observation-links o by follow)))
                                  (setf (gethash (obs-id o) (folded-by-id f)) r)
                                  r))
                    (folded-observations f)))
      (setf (folded-reflections f)
            (mapcar (lambda (x) (let ((r (resolve-reflection-citations x by follow)))
                                  (setf (gethash (refl-id x) (folded-refl-by-id f)) r)
                                  r))
                    (folded-reflections f)))
      (folded-derive f)))
  f)

(defun fold-ledger (entries &optional (up-to ""))
  "FoldLedger: the ledger through up-to (\"\" is the tip), every link read through level 1's
replacements (Standing)."
  (resolve-folded (fold-ledger-unresolved entries up-to)))

(defun consolidated-observation-ids (entries)
  "ConsolidatedObservationIDs: a set."
  (let ((seen (make-set)) (out (make-set)))
    (loop for e across entries
          do (multiple-value-bind (list covers consolidated) (recorded-data e)
               (when covers
                 (dolist (o list)
                   (unless (gethash (obs-id o) seen)
                     (setf (gethash (obs-id o) seen) t)
                     (when consolidated (setf (gethash (obs-id o) out) t)))))))
    out))

;;; Projections

(defstruct projection (observations '()) (reflections '()) (aliases '()))

(defun boundary-index (entries index boundary)
  "boundary is :tip, :none or an entry id."
  (case boundary
    (:tip (1- (length entries)))
    (:none -1)
    (t (or (gethash boundary index) -1))))

(defun covered-at-or-before-p (covers index boundary-index)
  (let ((at (gethash covers index)))
    (and at (>= at 0) (>= boundary-index 0) (<= at boundary-index))))

(defun fold-projection (entries observations-boundary reflections-boundary drops-boundary)
  "foldProjection."
  (let* ((index (entry-index-by-id entries))
         (ob (boundary-index entries index observations-boundary))
         (rb (boundary-index entries index reflections-boundary))
         (db (boundary-index entries index drops-boundary))
         (observations '()) (reflections '()) (seen-o (make-set)) (seen-r (make-set))
         (dropped (make-set)) (by-id (make-set)) (aliases '())
         (tracker (make-tracker (lambda (id) (gethash id by-id))
                                (lambda (id) (hidden-by-p id observations dropped reflections)))))
    (loop for e across entries for i from 0
          do (block entry
               (multiple-value-bind (list covers consolidated) (recorded-data e)
                 (when (and covers (covered-at-or-before-p covers index ob))
                   (dolist (o list)
                     (unless (gethash (obs-id o) seen-o)
                       (setf (gethash (obs-id o) seen-o) t (gethash (obs-id o) by-id) o)
                       (push o observations)))
                   (tracker-recorded tracker list covers consolidated)
                   (return-from entry)))
               (multiple-value-bind (list covers) (reflections-data e)
                 (when (and covers (covered-at-or-before-p covers index rb))
                   (dolist (r list)
                     (unless (gethash (refl-id r) seen-r)
                       (setf (gethash (refl-id r) seen-r) t)
                       (push r reflections)))
                   (return-from entry)))
               (multiple-value-bind (ids covers consolidated) (dropped-data e)
                 (when covers
                   (when (covered-at-or-before-p covers index db)
                     (dolist (id ids) (setf (gethash id dropped) t))
                     (tracker-dropped tracker ids covers consolidated))
                   (return-from entry)))
               (multiple-value-bind (list through) (subjects-aliased-data e)
                 (when (and list
                            (if (nth-value 1 (gethash through index))
                                (covered-at-or-before-p through index ob)
                                (and (>= ob 0) (<= i ob))))
                   (setf aliases (append aliases list))))))
    (setf observations (nreverse observations) reflections (nreverse reflections))
    (multiple-value-setq (observations reflections)
      (maintain-truth observations dropped (tracker-by tracker) reflections))
    (let ((by (tracker-by tracker)))
      (make-projection
       :observations (loop for o in observations
                           unless (gethash (obs-id o) dropped)
                             collect (resolve-observation-links o by))
       :reflections (mapcar (lambda (r) (resolve-reflection-citations r by)) reflections)
       :aliases aliases))))

(defun full-projection* (entries &optional (up-to ""))
  "FullProjection as a projection."
  (let ((b (if (string= up-to "") :tip up-to)))
    (fold-projection entries b b b)))

(defun full-projection (entries &optional (up-to ""))
  "FullProjection: (values observations reflections aliases)."
  (let ((p (full-projection* entries up-to)))
    (values (projection-observations p) (projection-reflections p) (projection-aliases p))))

;;; Compaction details (om.folded)

(defstruct details
  (full-fold nil) (observations '()) (reflections '()) (fold-through "") (summary-codec "")
  (summary-format "") (aliases '()) (engine "") consolidation (shown nil))

(defun memory-details-from (m)
  "MemoryDetailsFrom: the details, or nil."
  (when (hash-table-p m)
    (let ((version (jget m "version")) (full-fold (jget m "fullFold")))
      (multiple-value-bind (observations ok-obs) (observation-list (jget m "observations") nil)
        (multiple-value-bind (reflections ok-ref) (reflection-list (jget m "reflections") nil)
          (multiple-value-bind (through present) (gethash "foldThroughEntryId" m)
            (when (and (equal (jget m "type") "om.folded") (realp version) (= version 1)
                       (member full-fold '(:true :false)) ok-obs ok-ref
                       (or (not present) (stringp through) (jnull-p through)))
              (make-details :full-fold (jtrue-p full-fold) :observations observations :reflections reflections
                            :fold-through (str through) :summary-codec (str (jget m "summaryCodec"))
                            :summary-format (str (jget m "summaryFormat"))
                            :aliases (subject-alias-list (jget m "aliases"))
                            :engine (str (jget m "engine"))
                            :consolidation (let ((c (jget m "consolidation"))) (and (hash-table-p c) c))
                            :shown (let ((s (jget m "shown")))
                                     (and (json-array-p s) (remove-if-not #'stringp (coerce s 'list))))))))))))

(defun latest-compaction-details (entries)
  "latestV3CompactionDetails: the newest compaction's details, or nil."
  (loop for i from (1- (length entries)) downto 0
        do (when (string= (entry-type (aref entries i)) "compaction")
             (return (memory-details-from (entry-details (aref entries i)))))))

(defun latest-full-fold-boundary-id (entries)
  "LatestFullFoldBoundaryID."
  (let ((index (entry-index-by-id entries)))
    (loop for i from (1- (length entries)) downto 0
          for e = (aref entries i)
          do (when (string= (entry-type e) "compaction")
               (let ((d (memory-details-from (entry-details e))))
                 (when (and d (details-full-fold d))
                   (when (and (string/= (details-fold-through d) "") (nth-value 1 (gethash (details-fold-through d) index)))
                     (return-from latest-full-fold-boundary-id (details-fold-through d)))
                   (when (and (string/= (entry-first-kept-id e) "") (nth-value 1 (gethash (entry-first-kept-id e) index)))
                     (return-from latest-full-fold-boundary-id (entry-first-kept-id e)))))))
    ""))

(defun build-compaction-projection (entries first-kept pool-max-tokens)
  "BuildCompactionProjection: (values projection full-fold details); pool-max-tokens nil is
no limit."
  (let* ((boundary (latest-full-fold-boundary-id entries))
         (maintenance (if (string= boundary "") :none boundary))
         (normal (fold-projection entries first-kept maintenance maintenance))
         (tokens (reduce #'+ (projection-observations normal) :key #'obs-token-count))
         (full-fold (and pool-max-tokens (>= tokens pool-max-tokens)))
         (projection (if full-fold (full-projection* entries first-kept) normal)))
    (values projection full-fold
            (make-details :full-fold full-fold :observations (projection-observations projection)
                          :reflections (projection-reflections projection) :fold-through first-kept
                          :aliases (projection-aliases projection)))))

(defun visible-projection (entries &optional (up-to ""))
  "VisibleProjection."
  (if (string= up-to "")
      (let ((d (latest-compaction-details entries)))
        (if d
            (make-projection :observations (details-observations d) :reflections (details-reflections d)
                             :aliases (details-aliases d))
            (make-projection)))
      (values (build-compaction-projection entries up-to nil))))

;;; Progress (progress.go)

(defun entry-index-for-id (entries id)
  (if (string= id "") -1 (or (gethash id (entry-index-by-id entries)) -1)))

(defun latest-coverage-index (entries custom-type)
  "LatestCoverageIndex."
  (let ((index (entry-index-by-id entries)) (latest -1))
    (loop for e across entries
          do (let ((covers (and (custom-of-p e custom-type)
                                (cond ((string= custom-type "om.observations.recorded") (nth-value 1 (recorded-data e)))
                                      ((string= custom-type "om.reflections.recorded") (nth-value 1 (reflections-data e)))
                                      ((string= custom-type "om.observations.dropped") (nth-value 1 (dropped-data e)))
                                      ((string= custom-type "om.extracted") (extracted-data e))))))
               (when covers
                 (let ((at (gethash covers index)))
                   (when (and at (> at latest)) (setf latest at))))))
    latest))

(defun extraction-progress-index (entries)
  (max (latest-coverage-index entries "om.observations.recorded") (latest-coverage-index entries "om.extracted")))

(defun find-last-compaction-index (entries)
  (or (position "compaction" entries :key #'entry-type :test #'string= :from-end t) -1))

(defun compaction-range-start-index (entries)
  (let ((at (find-last-compaction-index entries)))
    (if (= at -1)
        0
        (let ((kept (entry-index-for-id entries (entry-first-kept-id (aref entries at)))))
          (if (= kept -1) (1+ at) kept)))))

(defun extraction-start-index (entries)
  "ExtractionStartIndex."
  (let ((start (max (1+ (extraction-progress-index entries)) (compaction-range-start-index entries)))
        (fold (entry-index-for-id entries (latest-full-fold-boundary-id entries))))
    (if (> fold start) fold start)))
