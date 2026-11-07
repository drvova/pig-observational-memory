;;;; Level 1's part of the Go layer (<curated store>/harness): the consolidation instructions the
;;;; level-1 system prompt carries (declare.Active.Instructions over layer.Store.Load: written,
;;;; then held under memory v2, for all models then the model), the offer's evidence
;;;; (rules.ProposalDue, V2EvidenceOf, Evidence.Text and Digest, layer.Store.LastProposal), the
;;;; hold (rules.CheckV2Proposal, sameAsSeed, harness.CheckModelDefinition with
;;;; rules.CheckFromModel's provenance check, layer.Store.HoldPass and HoldLocked, with the
;;;; propose generation), and the settle (settleDefinitionsV2 with rules.SettleV2 and
;;;; layer.Store.ClaimHeld). The files, blobs, held index and manifest lines are Go's, so the Go
;;;; engine reads, applies and settles what the image holds, and the other way round.
;;;;
;;;; Decision 5: only instructions/consolidation is ever held here; the image holds no detector,
;;;; remedy or rule.

(in-package "OM.GOLAYER")

(defparameter +no-fallback+ "none")
(defparameter +engine-v2+ "v2")
(defparameter +op-propose+ "propose")
(defparameter +op-revert+ "revert")

(defmacro push-end* (item place) `(setf ,place (append ,place (list ,item))))

;;; Modes (layer.SplitMode, liftMode)

(defun split-mode (text)
  "SplitMode: (values mode body error)."
  (let* ((trimmed (string-left-trim '(#\Space #\Tab #\Return #\Newline) text))
         (nl (position #\Newline trimmed))
         (first (if nl (subseq trimmed 0 nl) trimmed))
         (rest (if nl (subseq trimmed (1+ nl)) ""))
         (line (om::trim-space first))
         (colon (position #\: line)))
    (if (or (null colon) (not (string-equal (om::trim-space (subseq line 0 colon)) "mode")))
        (values "" text nil)
        (let* ((value (om::trim-space (subseq line (1+ colon)))) (mode (string-downcase value)))
          (if (member mode (list +mode-extend+ +mode-replace+) :test #'string=)
              (values mode (string-left-trim '(#\Return #\Newline) rest) nil)
              (values "" text (format nil "the opening line ~A names mode ~A; an instructions mode is ~A (the default: your text is appended after the seed instructions) or ~A (your text takes the seed instructions' place)"
                                      (om::go-quote (om::trim-space first)) (om::go-quote value) +mode-extend+ +mode-replace+)))))))

(defun lift-mode (d)
  "liftMode: (values d nil) or (values nil refusal)."
  (let ((d (om.layer::copy-gdef d)))
    (multiple-value-bind (mode body err) (split-mode (s0 (om.layer::gdef-text d)))
      (when err (return-from lift-mode (values nil err)))
      (when (string/= mode "")
        (when (or (string/= (s0 (om.layer::gdef-mode d)) "") (string/= (split-mode body) ""))
          (return-from lift-mode
            (values nil (format nil "the instructions definition ~A names its mode twice; open the text with one mode line"
                                (om::go-quote (om.layer::gdef-name d))))))
        (setf (om.layer::gdef-mode d) mode (om.layer::gdef-text d) body)))
    (let ((refusal (check-mode d))) (if refusal (values nil refusal) (values d nil)))))

(defun stamp-instructions (d)
  "Store.stamp for an instructions text: trimmed, its mode lifted, checked, timed, seeded."
  (let ((d (om.layer::copy-gdef d)))
    (setf (om.layer::gdef-text d) (trim-right-newlines (s0 (om.layer::gdef-text d))))
    (multiple-value-bind (lifted refusal) (lift-mode d)
      (if refusal (values nil refusal) (stamp-lifted lifted)))))

(defun declared (d)
  "Definition.Declared: an instructions text opens with its mode line."
  (let ((mode (s0 (om.layer::gdef-mode d))))
    (cat "mode: " (if (string= mode "") +mode-extend+ mode) (string #\Newline) (s0 (om.layer::gdef-text d)))))

(defun instructions-held-id (d) (cat (def-key d) "@" (digest (declared d))))

(defun relation (d)
  "Definition.Relation."
  (cond ((string= (s0 (om.layer::gdef-seed d)) "") "a new definition")
        ((def-replaces-p d) (cat "a replacement of the seed " (om.layer::gdef-seed d) ", after the kernel's rules and on probation"))
        (t (cat "an addition to the seed " (om.layer::gdef-seed d) ", appended after it"))))

;;; Reading the held files

(defun held-file (store model kind name)
  "Store.load of a held file under memory v2: the definition (state :held), or nil."
  (let ((d (load-file (held-path store model kind name) model kind name (held-index store model))))
    (when (and d (string= (s0 (om.layer::gdef-engine d)) +engine-v2+))
      (setf (om.layer::gdef-state d) :held)
      d)))

(defun held-v2-p (store model)
  "Whether model holds any definition memory v2 settles (rules.ProposalDue's wait): a held file
the held index records, whose header names engine v2."
  (let ((index (held-index store model)))
    (dolist (kind +kinds+ nil)
      (dolist (name (rows-names (om.layer::join-path (model-path store model) "held" kind)))
        (let* ((data (read-octets (held-path store model kind name)))
               (d (and data (decode-file (octets-text data)))))
          (when (and d (string= (s0 (om.layer::gdef-engine d)) +engine-v2+)
                     (string= (recorded index (seed-key kind name)) (digest-octets data)))
            (return-from held-v2-p t)))))))

(defun consolidation-instructions (store model &key (repo "") (family ""))
  "declare.Active.Instructions(consolidation) over the effective layer, scoped to repo and family
(layer.Harness.InScope): the text of the model's written, then held (v2) consolidation
instructions, for all models then the model; a written one whose settled scope does not cover
repo and family is left out; \"\" while the seed stands."
  (let ((text ""))
    (when (and store (string/= store "") (string/= model ""))
      (dolist (m (if (string= model +all-models+) (list +all-models+) (list +all-models+ model)))
        (let ((written (find +instructions-consolidation+ (list-defs store m +kind-instructions+)
                             :key #'om.layer::gdef-name :test #'string=)))
          (when (and written (scope-covers-p (written-scope store m +kind-instructions+ +instructions-consolidation+) repo family))
            (setf text (s0 (om.layer::gdef-text written)))))
        (let ((held (held-file store m +kind-instructions+ +instructions-consolidation+)))
          (when held (setf text (s0 (om.layer::gdef-text held)))))))
    text))

(defun held-instructions-snapshot (store model)
  "The model's own held consolidation instructions as the compaction reads them before level 1
(settleDefinitionsV2's snapshot), a list of gdefs."
  (let ((d (and store (string/= store "") (string/= model "")
                (held-file store model +kind-instructions+ +instructions-consolidation+))))
    (and d (list d))))

;;; The evidence (rules.V2EvidenceOf, Evidence.Text, Digest)

(defstruct evidence (counts '()) (passes 0) (tokens 0) (rules '()) (events 0))

(defun v2-evidence (store model)
  (let* ((events (om.layer::model-events store model))
         (ev (coerce events 'simple-vector))
         (passes (om.layer::passes-from events 0))
         (counts (make-hash-table :test 'equal)) (e (make-evidence :events (length events))))
    (when (> (length passes) om.layer::+min-evidence+)
      (setf passes (last passes om.layer::+min-evidence+)))
    (setf (evidence-passes e) (length passes))
    (dolist (i passes)
      (incf (evidence-tokens e) (om.layer::ev-int (aref ev i) "tokens"))
      (loop for (k . n) in (om.layer::ev-counts (aref ev i) "counts")
            when (plusp n) do (incf (gethash k counts 0) n)))
    ;; definedCounts: the defined:<name> counts of the detector passes that belong to a window pass.
    (let ((owner -1))
      (loop for x across ev for i from 0
            do (cond ((om.layer::v2-compaction-p x) (setf owner i))
                     ((and (string= (om.layer::ev-string x "kind") "detector-pass")
                           (string= (om.layer::ev-string x "engine") "v2") (member owner passes))
                      (loop for (k . n) in (om.layer::ev-counts x "counts")
                            when (and (plusp n) (om::has-prefix k "defined:")) do (incf (gethash k counts 0) n))))))
    (let ((alist '()))
      (maphash (lambda (k n) (push (cons k n) alist)) counts)
      (setf (evidence-counts e) (sort alist #'string< :key #'car)))
    (setf (evidence-rules e)
          (sort (mapcar (lambda (r) (cat (om.layer::rule-describe r) " " (om.layer::rule-state r)))
                        (om.layer::active-rules store model))
                #'string<))
    e))

(defun evidence-digest (e)
  (let ((keys (loop for (k . n) in (evidence-counts e) when (plusp n) collect k)))
    (digest (cat (om::join keys ",") (string (code-char #x1f)) (om::join (evidence-rules e) (string (code-char #x1f)))))))

(defun evidence-text (e)
  (with-output-to-string (out)
    (format out "Counted over your last ~D compactions (~D source tokens level 0 read):~%" (evidence-passes e) (evidence-tokens e))
    (if (null (evidence-counts e))
        (format out "- no detector counted anything~%")
        (loop for (k . n) in (evidence-counts e) do (format out "- ~A: ~D~%" k n)))
    (if (null (evidence-rules e))
        (format out "Rules applied to you: none~%")
        (format out "Rules applied to you:~%- ~A~%" (om::join (evidence-rules e) (format nil "~%- "))))))

(defun harness-evidence-text (e store model)
  "harnessEvidenceText as the image offers it (decision 5): the evidence, the model's own v2
definitions that stand (held ones; the offer waits while any is held, so none), and what it may
define: the consolidation instructions only."
  (let ((own (let ((d (held-file store model +kind-instructions+ +instructions-consolidation+)))
               (and d (list (format nil "- ~A (held)" (def-key d)))))))
    (cat (evidence-text e)
         (if own
             (cat "Your definitions:" (string #\Newline) (om::join own (string #\Newline)) (string #\Newline))
             (cat "Your definitions: none; every name stands as its seed" (string #\Newline)))
         (format nil "You may define: ~A (instructions only ~A, added after the kernel's rules and the seed, which they extend and never replace)."
                 +kind-instructions+ (om::go-quote +instructions-consolidation+)))))

(defun last-proposal-evidence (store model)
  "layer.Store.LastProposal's evidence, or nil when model has no proposal."
  (let ((last nil))
    (dolist (g (generations store model) last)
      (when (equal (om::jget g "op") +op-propose+)
        (setf last (let ((ev (om::jget g "evidence"))) (if (stringp ev) ev "")))))))

(defun proposal-due (store model)
  "rules.ProposalDue: (values evidence due-p)."
  (if (string= model "")
      (values nil nil)
      (let ((e (v2-evidence store model)))
        (if (held-v2-p store model)
            (values e nil)
            (values e (string/= (evidence-digest e)
                                (or (last-proposal-evidence store model) (evidence-digest (make-evidence)))))))))

;;; The checks (rules.CheckV2Proposal, sameAsSeed, harness.CheckModelDefinition)

(defun provenance-words (text)
  "provenance.Words: lowercased, split at every character that is not a letter or a digit."
  (om::fields-func (string-downcase text) (lambda (c) (not (or (om::letter-p c) (om::number-p c))))))

(defun provenance-normalize (text) (string-downcase (om::join (om::fields text) " ")))

(defun provenance-block-text (content)
  (cond ((stringp content) content)
        ((and (vectorp content) (not (stringp content))) (om::text-only content))
        (t "")))

(defun provenance-outputs (branch)
  "provenance.Outputs: the texts on branch the model read rather than wrote, normalized."
  (let ((out '()))
    (loop for e across (coerce branch 'simple-vector)
          do (let* ((type (om::entry-type e)) (m (om::entry-message e)) (role (and (hash-table-p m) (om::jget m "role")))
                    (haystack (cond ((and (string= type "message") (hash-table-p m) (equal role "bashExecution"))
                                     (om::bash-execution-text m))
                                    ((and (string= type "message") (hash-table-p m) (not (member role '("user" "assistant") :test #'equal)))
                                     (provenance-block-text (om::jget m "content")))
                                    ((string= type "custom_message") (provenance-block-text (om::entry-content e)))
                                    ((member type '("branch_summary" "compaction") :test #'string=)
                                     (if (stringp (om::entry-summary e)) (om::entry-summary e) ""))
                                    (t nil))))
               (when haystack
                 (let ((n (provenance-normalize haystack))) (when (string/= n "") (push n out))))))
    (nreverse out)))

(defun copied-run (outputs text seed)
  (let* ((seed-words (cat " " (om::join (provenance-words seed) " ") " "))
         (words (coerce (provenance-words text) 'simple-vector))
         (runs (make-hash-table :test 'equal)) (firsts (make-hash-table :test 'equal)))
    (loop for i from 0 to (- (length words) 6)
          do (let ((run (om::join (coerce (subseq words i (+ i 6)) 'list) " ")))
               (unless (search (cat " " run " ") seed-words)
                 (setf (gethash run runs) t (gethash (aref words i) firsts) t))))
    (when (zerop (hash-table-count runs)) (return-from copied-run nil))
    (dolist (output outputs nil)
      (let ((ow (coerce (provenance-words output) 'simple-vector)))
        (loop for i from 0 to (- (length ow) 6)
              do (when (gethash (aref ow i) firsts)
                   (let ((run (om::join (coerce (subseq ow i (+ i 6)) 'list) " ")))
                     (when (gethash run runs) (return-from copied-run run)))))))))

(defun copied-segment (branch text seed)
  "provenance.CopiedSegment: the first part of text found in output on branch, or nil."
  (let ((text (om::trim-space text)))
    (when (string= text "") (return-from copied-segment nil))
    (let ((outputs (provenance-outputs branch)))
      (when (null outputs) (return-from copied-segment nil))
      (let ((whole (provenance-normalize text)) (nseed (provenance-normalize seed)))
        (if (and (not (and (string/= nseed "") (search whole nseed)))
                 (string/= whole "") (some (lambda (h) (search whole h)) outputs))
            text
            (copied-run outputs text seed))))))

(defun clip-runes (s n) (if (<= (length s) n) s (cat (subseq s 0 n) "…")))

(defun check-level1-definition (kind name text model branch)
  "rules.CheckV2Proposal, sameAsSeed and harness.CheckModelDefinition for one definition level 1
proposed: the refusal's message, or nil. Validation offered instructions only."
  (unless (string= kind +kind-instructions+)
    (return-from check-level1-definition "only the consolidation instructions are offered"))
  (unless (string= name +instructions-consolidation+)
    (return-from check-level1-definition
      (format nil "the only instructions are ~A, the level-1 request's; no worker stage runs" (om::go-quote +instructions-consolidation+))))
  (multiple-value-bind (mode body err) (split-mode text)
    (when err (return-from check-level1-definition err))
    (when (string= mode +mode-replace+)
      (return-from check-level1-definition
        (format nil "the ~A instructions extend the seed and never replace it; drop the \"mode: replace\" line"
                (om::go-quote +instructions-consolidation+))))
    (let ((seed (seed-text (seed-key kind name))))
      (when (and seed (string= (om::trim-space seed) (om::trim-space body)))
        (return-from check-level1-definition "identical to the seed, which already stands; define only what you would change"))))
  (multiple-value-bind (lifted refusal) (lift-mode (om.layer::make-gdef :kind kind :name name :model model :text text))
    (when refusal (return-from check-level1-definition refusal))
    (let ((refusal (check-stored lifted))) (when refusal (return-from check-level1-definition refusal))))
  (let ((quoted (copied-segment branch text (or (seed-text (seed-key kind name)) ""))))
    (when quoted
      (format nil "refused: the ~A definition ~A quotes text found in tool output, a file or another message on this branch (~A); a definition's text must be your own words"
              kind name (om::go-quote (clip-runes quoted 80))))))

;;; Holding (layer.Store.HoldPass, HoldLocked, standingFallback)

(defun standing-fallback (store model kind name)
  (let ((data (read-octets (def-path store model kind name))))
    (if (null data)
        +no-fallback+
        (let* ((d (digest-octets data)) (blob (blob-path store model d)))
          (unless (path-exists-p blob) (write-atomic blob (octets-text data)))
          d))))

(defun hold-locked (store d)
  "HoldLocked under memory v2: (values held-gdef nil) or (values nil refusal)."
  (multiple-value-bind (d refusal) (stamp-instructions d)
    (when refusal (return-from hold-locked (values nil refusal)))
    (setf (om.layer::gdef-engine d) +engine-v2+)
    (let* ((model (om.layer::gdef-model d)) (kind (om.layer::gdef-kind d)) (name (om.layer::gdef-name d))
           (path (held-path store model kind name)) (fallback "")
           (old (read-octets path)))
      (when old
        (let ((prev (decode-file (octets-text old))))
          (when (and prev (string/= (s0 (om.layer::gdef-fallback prev)) ""))
            (setf fallback (om.layer::gdef-fallback prev)))))
      (when (string= fallback "") (setf fallback (standing-fallback store model kind name)))
      (let ((content (encode-file d fallback)))
        (write-atomic path content)
        (record-held store model (def-key d) (digest content)))
      (setf (om.layer::gdef-state d) :held)
      (values d nil))))

(defun append-propose (store model &key evidence events session)
  "Mark's OpPropose generation: gen, time, op, session, reason \"level 1\", evidence, events."
  (let ((gen (1+ (length (generations store model)))))
    (om.layer::append-line
     (om.layer::join-path (model-path store model) "manifest.jsonl")
     (with-output-to-string (out)
       (format out "{\"gen\":~D,\"time\":" gen)
       (om::write-json-string (om.layer::format-rfc3339-nano (cons (om.layer::time-nanos (now)) 0)) out)
       (write-string ",\"op\":\"propose\"" out)
       (when (and session (string/= session "")) (write-string ",\"session\":" out) (om::write-json-string session out))
       (write-string ",\"reason\":\"level 1\"" out)
       (when (and evidence (string/= evidence "")) (write-string ",\"evidence\":" out) (om::write-json-string evidence out))
       (when (and events (plusp events)) (format out ",\"events\":~D" events))
       (write-char #\} out)))
    gen))

(defun hold-pass (store model defs evidence session)
  "HoldPass with the level-1 mark: holds defs (gdefs for model) and appends the propose
generation in one critical section. (values held refusals), refusals a list parallel to defs."
  (let ((held '()) (refusals '()))
    (with-locked (store)
      (dolist (d defs)
        (multiple-value-bind (h refusal) (hold-locked store d)
          (push-end* refusal refusals)
          (when h (push-end* h held))))
      (append-propose store model :evidence (evidence-digest evidence) :events (evidence-events evidence) :session session))
    (values held refusals)))

;;; Settling (settleDefinitionsV2, ClaimHeld, claimOne, restore)

(defun standing (store model kind name)
  (cond ((path-exists-p (def-path store model kind name)) "the previous definition")
        ((seed-text (seed-key kind name)) "the seed")
        (t "nothing")))

(defun restore (store model kind name target)
  "restore: model's file for kind/name made to hold the blob target names (\"\" removes it); the
digest written, :removed, or nil when it already did."
  (let* ((path (def-path store model kind name)) (data (read-octets path)) (have (if data (digest-octets data) "")))
    (cond ((string= have target) nil)
          ((string= target "") (ignore-errors (delete-file path)) :removed)
          (t (let ((blob (read-octets (blob-path store model target))))
               (unless blob (error "the content of ~A/~A at that generation is missing" kind name))
               (write-atomic path (octets-text blob))
               target)))))

(defun claim-held (store d clean reason &optional scope)
  "ClaimHeld for one held definition the compaction ran with: settled only while its held file
still holds that text and mode under v2. Returns the notice text, or nil."
  (with-locked (store)
    (let* ((model (om.layer::gdef-model d)) (kind (om.layer::gdef-kind d)) (name (om.layer::gdef-name d))
           (path (held-path store model kind name)) (data (read-octets path)))
      (when (null data) (return-from claim-held nil))
      (let ((now (decode-file (octets-text data))))
        (when (and now (or (string/= (s0 (om.layer::gdef-text now)) (trim-right-newlines (s0 (om.layer::gdef-text d))))
                           (string/= (s0 (om.layer::gdef-mode now)) (s0 (om.layer::gdef-mode d)))
                           (string/= (s0 (om.layer::gdef-engine now)) +engine-v2+)))
          (return-from claim-held nil)))
      (let ((loaded (held-file store model kind name)) (fallback ""))
        (when (null loaded)
          (setf clean nil reason "its held file no longer passes the layer's checks")
          (let ((raw (decode-file (octets-text data)))) (when raw (setf fallback (s0 (om.layer::gdef-fallback raw))))))
        (when loaded (setf fallback (s0 (om.layer::gdef-fallback loaded))))
        (cond
          (clean
           (multiple-value-bind (stamped refusal) (stamp-lifted loaded)
             (when refusal (error "settle ~A/~A: ~A" kind name refusal))
             (put-stamped store stamped scope))
           (ignore-errors (delete-file path))
           (record-held store model (seed-key kind name) "")
           (format nil "Observational memory: held ~A definition ~A for ~A passed a clean pass and is written to the layer (generation ~D)"
                   kind name model (length (generations store model))))
          (t
           (ignore-errors (delete-file path))
           (record-held store model (seed-key kind name) "")
           (unless (string= fallback "")
             (let ((change (restore store model kind name (if (string= fallback +no-fallback+) "" fallback))))
               (when change
                 (append-generation store model +op-revert+ :kind kind :name name
                                                            :digest (if (stringp change) change nil) :reason reason))))
           (format nil "Observational memory: held ~A definition ~A for ~A was reverted (~A); ~A stands"
                   kind name model reason (standing store model kind name))))))))

(defun settle-held-instructions (store model snapshot)
  "settleDefinitionsV2 for the held instructions the compaction ran with (snapshot, read before
level 1): each one rules.SettleV2 decides is written when clean and reverted otherwise. Returns
the notices."
  (when snapshot
    (let ((events (om.layer::model-events store model)) (notices '()))
      (dolist (d snapshot (nreverse notices))
        (multiple-value-bind (decided clean reason scope)
            (om.layer::settle-v2 events :kind +kind-instructions+ :name (om.layer::gdef-name d)
                                        :held-id (instructions-held-id d) :time (om.layer::gdef-time d)
                                        :text (declared d))
          (when decided
            (let ((n (claim-held store d clean reason scope))) (when n (push n notices)))))))))

;;; Decision 24 (memory/layer/settle_v2.go, memory/hooks/definitions_v2.go): the read-time scope
;;; a settlement records on its put, the hold that keeps a security-weakening definition out of
;;; the layer, and the rollback a security hit makes.

(defun go-states (store model)
  "manifest.go states: a vector, element n the kind/name -> digest map after generation n."
  (let ((gens (generations store model)) (out (list (make-hash-table :test 'equal))))
    (dolist (g gens)
      (let ((next (make-hash-table :test 'equal)) (op (om::jget g "op")) (target (om::jget g "target"))
            (prev (first out)))
        (if (and (string= op "rollback") (realp target) (>= target 0) (< target (length out)))
            (let ((keep (om::jget g "keep")))
              (maphash (lambda (k v) (setf (gethash k next) v)) (nth (- (length out) 1 (truncate target)) out))
              (when (and (vectorp keep) (not (stringp keep)))
                (loop for k across keep when (stringp k)
                      do (multiple-value-bind (v ok) (gethash k prev)
                           (if ok (setf (gethash k next) v) (remhash k next))))))
            (let ((key (seed-key (om::str (om::jget g "kind")) (om::str (om::jget g "name"))))
                  (d (om::str (om::jget g "digest"))))
              (maphash (lambda (k v) (setf (gethash k next) v)) prev)
              (cond ((or (string= op "forget") (and (string= op "revert") (string= d ""))) (remhash key next))
                    ((or (string= op "put") (string= op "revert")
                         (and (string= op "migrate") (not (eq (om::jget g "held") :true))))
                     (setf (gethash key next) d)))))
        (push next out)))
    (coerce (nreverse out) 'simple-vector)))

(defun put-generation-index (gens key d) (om.layer::go-put-generation-index gens key d))

(defun written-scope (store model kind name) (om.layer::go-written-scope store model kind name))

(defun scope-covers-p (scope repo family) (om.layer::scope-covers-p scope repo family))

(defun written-ids (store model)
  "The held ids of model's own written definitions the image reads (its consolidation
instructions and remedies): recordCompactionV2's ranWritten."
  (when (and store (string/= store "") (string/= model ""))
    (append (loop for d in (list-defs store model +kind-instructions+) collect (instructions-held-id d))
            (loop for d in (om.layer::go-list-remedies store model)
                  when (string= (om.layer::gdef-model d) model) collect (om.layer::gdef-held-id d)))))

(defun hold-insecure (store d rule)
  "layer.Store.HoldInsecure: d's content kept in the model's blobs and an OpHoldInsecure
generation, no held file; returns the generation's number."
  (with-locked (store)
    (let* ((model (om.layer::gdef-model d)) (d (om.layer::copy-gdef d)))
      (setf (om.layer::gdef-engine d) +engine-v2+ (om.layer::gdef-time d) (now))
      (let* ((content (encode-file d "")) (dg (digest content)))
        (write-atomic (blob-path store model dg) content)
        (append-generation store model "hold-insecure" :kind (om.layer::gdef-kind d) :name (om.layer::gdef-name d)
                                                       :digest dg :session (om.layer::gdef-session d)
                                                       :reason (cat "it weakens security (" rule "): held, never applied, settled or proposed over without a user's fresh quoted words"))))))

(defun rule-key-p (key)
  "rules.IsRule over a kind/name key: a remedy not named after a catalogue remedy."
  (let ((slash (position #\/ key)))
    (and slash (string= (subseq key 0 slash) "remedy")
         (not (member (subseq key (1+ slash)) om.layer::+remedies+ :test #'string=)))))

(defun rollback-locked (store model n keep reason)
  "rollbackKeeping with no session, the caller holding the lock: (values generation t), or
(values nil nil) when it would restore content a revert took away."
  (let* ((gens (generations store model)) (all (go-states store model))
         (target (aref all n)) (current (aref all (1- (length all))))
         (migrated (make-hash-table :test 'equal)))
    (loop for g in gens for i from 0
          when (and (equal (om::jget g "op") "migrate") (not (eq (om::jget g "held") :true))
                    (string/= (om::str (om::jget g "digest")) ""))
            do (let ((key (seed-key (om::str (om::jget g "kind")) (om::str (om::jget g "name")))))
                 (setf (gethash (cons key (om::str (om::jget g "digest"))) migrated)
                       (or (gethash key (aref all i)) ""))))
    (flet ((covers (key reverted d)
             (loop for x = reverted then (or (gethash (cons key x) migrated) "")
                   for steps from 0 to (length gens)
                   while (string/= x "")
                   when (string= x d) return t)))
      (loop for g in gens for i from 0
            when (equal (om::jget g "op") "revert")
              do (let* ((key (seed-key (om::str (om::jget g "kind")) (om::str (om::jget g "name"))))
                        (reverted (or (gethash key (aref all i)) ""))
                        (want (or (gethash key target) "")) (have (or (gethash key current) "")))
                   (unless (funcall keep key)
                     (when (and (string/= reverted "") (string/= want "") (covers key reverted want)
                                (not (covers key reverted have)))
                       (return-from rollback-locked (values nil nil)))))))
    (let ((keys '()) (keep-keys '()))
      (loop for state across all do (maphash (lambda (k v) (declare (ignore v)) (pushnew k keys :test #'string=)) state))
      (dolist (key (sort keys #'string<))
        (let ((slash (position #\/ key)))
          (if (funcall keep key)
              (unless (equal (gethash key target) (gethash key current)) (push-end* key keep-keys))
              (restore store model (subseq key 0 slash) (subseq key (1+ slash)) (or (gethash key target) "")))))
      (dolist (kind +kinds+)
        (dolist (name (rows-names (om.layer::join-path (model-path store model) "held" kind)))
          (unless (funcall keep (seed-key kind name))
            (ignore-errors (delete-file (held-path store model kind name)))
            (record-held store model (seed-key kind name) ""))))
      (values (append-generation store model "rollback" :target n :keep keep-keys :reason reason) t))))

(defun rollback-before (store model kind name reason)
  "layer.Store.RollbackBefore keeping the rules (rules.IsRule) but the name itself: (values
generation target), target nil when the name alone was reverted; nil when nothing is written
for the name."
  (with-locked (store)
    (let ((data (read-octets (def-path store model kind name))) (key (seed-key kind name)))
      (when data
        (let ((at (put-generation-index (generations store model) key (digest-octets data))))
          (multiple-value-bind (gen ok)
              (if (>= at 0)
                  (rollback-locked store model at (lambda (k) (and (string/= k key) (rule-key-p k))) reason)
                  (values nil nil))
            (if ok
                (values gen at)
                (progn (ignore-errors (delete-file (def-path store model kind name)))
                       (values (append-generation store model +op-revert+ :kind kind :name name :reason reason) nil)))))))))

(defun rollback-insecure (store model)
  "rollbackInsecureV2 for the definitions the image reads (the model's written consolidation
instructions): the layer rolled back to the generation before one that weakens security, on code
evidence (no model or tool request, so no quote gate applies). Returns the notices."
  (let ((notices '()))
    (loop repeat 8
          do (let* ((d (find +instructions-consolidation+ (list-defs store model +kind-instructions+)
                             :key #'om.layer::gdef-name :test #'string=))
                    (rule (and d (om::insecure-match (s0 (om.layer::gdef-text d))))))
               (unless rule (return))
               (let* ((key (def-key d))
                      (reason (format nil "security: ~A weakens security (~A); rolled back to the generation before it entered, on code evidence (no model or tool request, so no quote gate applies)" key rule))
                      )
                 (multiple-value-bind (gen target)
                     (rollback-before store model +kind-instructions+ +instructions-consolidation+ reason)
                   (unless gen (return))
                   (push (if target
                             (format nil "Observational memory: ~A's layer: ~D: rollback to ~D (~A)" model gen target reason)
                             (format nil "Observational memory: ~A's layer: ~D: revert ~A (~A)" model gen key reason))
                         notices)))))
    (nreverse notices)))

(export '(consolidation-instructions held-instructions-snapshot written-ids hold-insecure rollback-insecure instructions-held-id proposal-due
          harness-evidence-text check-level1-definition hold-pass relation settle-held-instructions
          evidence-digest def-key))
