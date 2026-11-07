;;;; Times the search behind the reads (src/retrieve.lisp) over a file of records, one JSON record
;;;; per line, as memory/retrieve's TestSearchLatency writes them (OM_SEARCH_LATENCY_DIR):
;;;;   sbcl --dynamic-space-size 8192 --script lisp/search-latency.lisp <records.jsonl>
;;;; For each search it builds every document and searches, as a read does, and prints the time.
;;;; A measurement only; nothing compares it.

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

(in-package "OM")

(defparameter *latency-searches* '("retryWithCache7" "internal/store/cache.go" "2.6.10" "billing port" "parse config timeout"))

(let* ((path (car (last sb-ext:*posix-argv*)))
       (records (with-open-file (in path :external-format :utf-8)
                  (loop for line = (read-line in nil) while line
                        when (plusp (length line)) collect (observation-from (json-parse (coerce line 'simple-string))))))
       (tags (tags-of records)))
  (dolist (q *latency-searches*)
    (sb-ext:gc :full t)
    (let ((start (get-internal-real-time)))
      (let* ((pool (map 'vector (lambda (o) (doc-of o (obs-content o) (gethash (obs-id o) tags))) records))
             (results (retrieval-search pool (list q)))
             (ms (round (* 1000 (- (get-internal-real-time) start)) internal-time-units-per-second)))
        (format t "lisp n=~D search=~S results=~D first=~A time=~Dms~%" (length records) q (length results)
                (if results (rdoc-id (aref pool (first (first results)))) "") ms)))))
