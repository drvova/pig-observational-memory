;;;; Phase 8: delivery, dreams and erasure (docs/brain-memory.md; docs/memory-v2.md, "Cues",
;;;; "Revisions", "Dreams" and "Erasure and the layer purge"). The Go tests are
;;;; memory/layer/secret_test.go, memory/cue, memory/erase and the contract cases they share.

(in-package "OM")

(defun d-test-key () (concatenate 'string "AKIA" "Q7Q7Q7Q7" "ZX3V" "LMN5"))

(deftest delivery-layer-write-redacts-prose
  (let* ((value (d-test-key))
         (d (om.layer::make-gdef :kind "instructions" :name "consolidation" :model "p/m" :session "s0" :engine ""
                                 :time (om.layer::parse-rfc3339 "2026-11-06T08:30:00Z")
                                 :text (concatenate 'string "Keep the deploy line; AWS_ACCESS_KEY_ID=" value " stays out.")))
         (file (om.layer::go-encode-file d "")))
    (check (search value file) nil "no value in the file")
    (check (and (search "AWS_ACCESS_KEY_ID=[redacted:" file) t) t "the variable name kept")))

(deftest delivery-cue-contract-cases
  (let ((cases (contract-cases "cue.jsonl")))
    (check (> (length cases) 10) t "cases read")
    (dolist (c cases)
      (let* ((call (jget c "call"))
             (entries (map 'vector #'entry-from-map (jget c "entries")))
             (want (jget c "cue")))
        (multiple-value-bind (record key text)
            (cue-for entries (jget call "tool") (jget call "args") (or (jget call "output") "") (jget c "cwd") (jget c "cap"))
          (check (list record key text)
                 (if (hash-table-p want) (list (jget want "record") (jget want "key") (jget want "text")) (list nil nil nil))
                 (jget c "name")))))))

(deftest delivery-dream-contract-cases
  (let ((cases (contract-cases "dreams.jsonl")) (sections 0))
    (check (> (length cases) 5) t "cases read")
    (dolist (c cases)
      (let ((name (jget c "name")))
        (cond ((jget c "section")
               (incf sections)
               (check (dream-section (map 'vector #'entry-from-map (or (jget c "entries") (vector))))
                      (jget c "section") name))
              ((jget c "parameters")
               (multiple-value-bind (tool description parameters) (consolidate-tool nil nil t)
                 (declare (ignore tool))
                 (check description (jget c "description") (cat name ": description"))
                 (check (json-string parameters) (json-string (jget c "parameters")) (cat name ": parameters"))))
              ((jget c "shown")
               (let* ((candidates (map 'list (lambda (id) (make-obs :id id :content (cat "Record " id ".") :kind "fact"))
                                       (jget c "shown")))
                      (args (jobject "records" (vector (jobject "id" "0000000000d1" "relevance" "high"))
                                     "hypotheses" (jget c "args")))
                      (r (validate-level1 args (make-vinput :candidates candidates :folded (fold-ledger (vector)) :dreams t)))
                      (want (map 'list (lambda (h) (list (jget h "id") (jget h "content") (coerce (jget h "from") 'list)))
                                 (or (jget c "hypotheses") (vector)))))
                 (check (vresult-hypotheses r) want (cat name ": hypotheses"))
                 (check (count "hypothesis" (vresult-refusals r) :key #'first :test #'string=) (round (or (jget c "refused") 0))
                        (cat name ": refused"))
                 (check (vresult-hypotheses (validate-level1 args (make-vinput :candidates candidates :folded (fold-ledger (vector)))))
                        nil (cat name ": dreams off")))))))
    (check sections 3 "sections")))

(deftest delivery-layer-hold-refuses-code-with-secret
  (let ((value (d-test-key)))
    (check (handler-case
               (progn (om.layer::layer-hold "p/m" (concatenate 'string "(om.tool:define leaky () \"AWS_ACCESS_KEY_ID=" value "\")"))
                      :held)
             (om.layer::refused (c) (and (search "holds a secret" (om.layer::refused-reason c)) :refused)))
           :refused "refused before it is compiled or written")))
