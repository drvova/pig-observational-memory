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

(deftest delivery-layer-hold-refuses-code-with-secret
  (let ((value (d-test-key)))
    (check (handler-case
               (progn (om.layer::layer-hold "p/m" (concatenate 'string "(om.tool:define leaky () \"AWS_ACCESS_KEY_ID=" value "\")"))
                      :held)
             (om.layer::refused (c) (and (search "holds a secret" (om.layer::refused-reason c)) :refused)))
           :refused "refused before it is compiled or written")))
