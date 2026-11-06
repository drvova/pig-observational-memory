;;;; The Lisp side of the status parity tests (memory/contracts/lispstatus_test.go):
;;;;   sbcl --script lisp/status-parity.lisp status <config home> <model or -> <settings.json> <clock>
;;;;     prints /om:status over the curated store of <config home> with an empty branch, a session
;;;;     model of that key and no context window, the project root being <config home>, up to the
;;;;     image's own code section;
;;;;   sbcl --script lisp/status-parity.lisp profile <store dir> <model>
;;;;     prints harness.Ledger.Profile's figures: codec, layout, halvings, events, then each switch;
;;;;   sbcl --script lisp/status-parity.lisp migrate-rules <store dir> <clock>
;;;;     runs rules.migrate with the clock fixed;
;;;;   sbcl --script lisp/status-parity.lisp hint <queries.json>
;;;;     prints, per query, legacyquery.Hint's snippet and reason, Substitute with an input, and
;;;;     what legacyAsk reads, as JSON lines.

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

(in-package "OM.SERVER")

(defun out (s) (write-string s) (terpri))

(defun fixed-clock (clock)
  (let ((at (om.layer::parse-rfc3339 clock))) (lambda () (cons (car at) 0))))

(let* ((args (member-if (lambda (a) (member a '("status" "profile" "migrate-rules" "hint") :test #'string=)) sb-ext:*posix-argv*))
       (mode (first args)))
  (cond
    ((string= mode "status")
     (destructuring-bind (home model settings clock) (rest args)
       (let* ((om.layer::*now* (fixed-clock clock))
              (store (store-dir-of home))
              (model (if (string= model "-") "" model))
              (x (make-sctx :entries #() :trust :untrusted :store store :project (om.layer::project-root home)
                            :floor-owner (list :parity) :settings-path settings
                            :auto-compaction (let ((v (om::jget (pig-compaction-object settings) "enabled")))
                                               (if (member v '(:true :false)) (eq v :true) t))
                            :session-key model)))
         (let ((cfg (make-rcfg)))
           (apply-settings cfg settings)
           (setf (sx-passive x) (rcfg-passive cfg) (sx-removed x) (rcfg-removed cfg)
                 (sx-memory-format x) (rcfg-memory-format cfg))
           (unless (rcfg-curated-memory cfg) (setf (sx-store x) nil)))
         (handler-case (dolist (l (status-lines x)) (out l))
           (om.golayer::layer-not-ported (e) (out (format nil "not ported: ~A" e)))))))
    ((string= mode "profile")
     (destructuring-bind (store model) (rest args)
       (let ((p (om.layer::model-profile store model)))
         (out (format nil "codec ~A layout ~A halvings ~D events ~D" (om.layer::hp-codec p) (om.layer::hp-layout p)
                      (om.layer::hp-halvings p) (om.layer::hp-events p)))
         (dolist (s (om.layer::hp-switches p)) (out (om.layer::switch-text s))))))
    ((string= mode "migrate-rules")
     (destructuring-bind (store clock) (rest args)
       (let ((om.layer::*now* (fixed-clock clock)))
         (om.layer::migrate-legacy-rules store)
         (out "done"))))
    ((string= mode "hint")
     (let ((queries (om::json-parse (coerce (om.layer::read-file (second args)) 'simple-string))))
       (loop for q across queries
             do (multiple-value-bind (snippet reason) (om.legacy:hint q)
                  (multiple-value-bind (recalled searches ok) (om.legacy:legacy-ask q)
                    (out (om::json-string
                          (om::jobject "snippet" snippet "reason" reason
                                       "substituted" (om.legacy:substitute-input q "a b\"c")
                                       "ok" (if ok :true :false)
                                       "recalled" (coerce recalled 'simple-vector)
                                       "searches" (coerce searches 'simple-vector))
                          :escape-html nil)))))))))
