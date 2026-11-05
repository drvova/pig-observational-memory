;;;; selection and the rows codec.

(in-package "OM")

(deftest rows-quoting
  (check (quote-value "plain value") "plain value")
  (check (quote-value "") "\"\"")
  (check (quote-value " lead") "\" lead\"")
  (check (quote-value "a, b") "\"a, b\"")
  (check (quote-value (format nil "say \"hi\"~%\\")) "\"say \\\"hi\\\"\\n\\\\\"")
  (check (encode-table (make-rtable :name "notes" :fields '("id" "content") :rows '(("a" "") ("b" "x,y")) :absent '((nil t) (nil nil))))
         (format nil "notes[2]{id,content}:~%  a,~%  b,\"x,y\"")))

(deftest snippets
  (check (format-read "Notes" :from "e1" :through "e9") "om.Notes(om.From(`e1`).Through(`e9`))")
  (check (format-read "Notes" :where '(("kind" . "file")) :limit 30) "om.Notes(om.Kind(`file`).Limit(30))")
  (check (format-read "Commits") "om.Commits()")
  (check (snippet +read-notes+ 25 :since "2026-05-01" :until "2026-05-02") "om.Notes(om.Since(`2026-05-01`).Until(`2026-05-02`).Limit(25))")
  (check (snippet +read-notes+ 900) "om.Notes(om.Limit(200))"))

(deftest render-bounds
  (let* ((branch (branch-of (jobject "id" "u1" "timestamp" "2026-05-06T09:00:00Z" "role" "user" "text" "The budget is 5000 USD.")))
         (records (extract-all branch)))
    ;; No budget, no records: no summary.
    (check (summary-text (render-summary records (fold-ledger branch) branch 0)) "")
    (check (summary-text (render-summary '() (fold-ledger branch) branch 5000)) "")
    ;; The instructions alone exceed this budget.
    (check (summary-text (render-summary records (fold-ledger branch) branch (fixed-cost))) "")
    (let ((text (summary-text (render-summary records (fold-ledger branch) branch 5000))))
      (check (has-prefix text +tables-instructions+) t)
      (check (has-suffix text (format nil "notes[1]{id,when,kind,status,scope,content,superseded_by,source}:~%  ~A,2026-05-06,fact,,,The budget is 5000 USD.,,u1"
                                      (obs-id (first records))))
             t))))
