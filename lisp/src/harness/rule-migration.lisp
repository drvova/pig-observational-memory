;;;; rules.migrate (memory/rules/rule.go): the rules written before the layer
;;;; (<store>/rules/*.rows) moved into it under the store's lock. Each legacy file becomes a layer
;;;; remedy file of the same rule (a rule the layer already holds keeps the layer's) and is renamed
;;;; .migrated; one that is not a valid rule is renamed .refused. A failure leaves the rest to be
;;;; tried again. Go runs it before every read of the rules (rules.For, List, AfterPass, ...); the
;;;; image runs it where it refused such a store before: a turn end, a compaction and the pass
;;;; (docs/lisp-port-status.md).

(in-package "OM.LAYER")

(defun legacy-rule-files (store)
  "legacyNames: the .rows files (not directories) of <store>/rules, in file-name order."
  (loop for (name . dirp) in (om.golayer::list-dir (join-path store "rules"))
        when (and (not dirp) (om::has-suffix name ".rows")) collect name))

(defun migrate-legacy-rules (store)
  "rules.migrate over the curated store directory store; nil without legacy rules."
  (when (and store (string/= store "") (legacy-rule-files store))
    (handler-case
        (with-store-lock (store)
          (dolist (name (legacy-rule-files store))
            (let* ((file (join-path store "rules" name)) (suffix ".migrated")
                   (data (or (om.golayer::read-text file) (error "read ~A" file))))
              (handler-case
                  (let* ((r (rule-parse file data))
                         (model (or (rule-layer-model r) (error "rule ~A applies to no model" (rule-id r)))))
                    (unless (om.golayer::path-exists-p (go-def-path store model (rule-id r)))
                      (write-rule store r)))
                (error () (setf suffix ".refused")))
              (sb-posix:rename file (concatenate 'string file suffix)))))
      (error (e) (om.server::log-line "rules: legacy migration: ~A" e)))
    t))
