;;;; The retired defined queries with their text (memory/harness/retired.go RetiredQueries,
;;;; DefinedQueries' texts, curated.Store.RefusedDefinitions): every stored definition by name,
;;;; with its query and, for a pure read the strict formatter can translate, the equivalent
;;;; snippet (legacyquery.Hint), else why there is none; then the legacy files the layer refused,
;;;; with the refusal. /om:status's line and om.Retired() list them; none is ever run.

(in-package "OM.GOLAYER")

(defun definition-row-query (text)
  "The query cell of a definition's row (parseDefinition's d.Query)."
  (multiple-value-bind (decoded err) (try-rows-decode text)
    (if (or err (null decoded)) "" (values (tbl-cell (first decoded) 0 "query")))))

(defun definition-queries (store)
  "curated.Store.ListDefinitions as (name . query), in name order (the legacy files migrated first)."
  (migrate-definitions store)
  (sort (loop for d in (list-defs store +all-models+ +kind-tool+)
              when (parse-definition-p (om.layer::gdef-name d) (om.layer::gdef-text d))
                collect (cons (om.layer::gdef-name d) (definition-row-query (om.layer::gdef-text d))))
        #'string< :key #'car))

(defun refused-definition-reasons (store)
  "curated.Store.RefusedDefinitions: (name . reason) for each legacy file the layer refused."
  (migrate-definitions store)
  (let ((dir (legacy-dir store)))
    (loop for name in (legacy-rows dir ".rows.refused")
          collect (cons name
                        (let ((d (legacy-definition name (om.layer::join-path dir (cat name ".rows.refused")))))
                          (cond ((null d) "unreadable")
                                ((check-stored d))
                                (t "the layer refused it")))))))

(defstruct (retired-query (:conc-name rq-)) name origin (query "") (snippet "") (reason ""))

(defun retired-queries (store)
  "harness.RetiredQueries over the curated store directory."
  (append (loop for (name . query) in (definition-queries store)
                collect (multiple-value-bind (snippet reason) (om.legacy:hint query)
                          (make-retired-query :name name :origin "layer" :query query :snippet snippet :reason reason)))
          (loop for (name . reason) in (refused-definition-reasons store)
                collect (make-retired-query :name name :origin "legacy"
                                            :reason (cat "not migrated into the layer: " reason)))))

(defun retired-status-line (store)
  "harness.retiredStatusLine: nil when there are none or the store is unreadable."
  (let ((retired (handler-case (retired-queries store) (error () nil))))
    (when retired
      (format nil "Retired defined queries (~D; the query language was removed, so none is registered or run; om.Retired() lists them): ~A"
              (length retired)
              (om::join (loop for q in retired
                              collect (if (string/= (rq-snippet q) "")
                                          (cat (rq-name q) ": " (rq-snippet q))
                                          (cat (rq-name q) " (" (rq-reason q) ")")))
                        "; ")))))
