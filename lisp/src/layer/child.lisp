;;;; The memory tool in a child SBCL (docs/lisp-core.md, decision 9; docs/lisp-port-tool-layer.md,
;;;; "The memory tool in a child"). A memory tool program runs in a child process started from the
;;;; image's own core (the dumped image with --tool-child, or sbcl loading the system when the
;;;; image runs from source), with a small --dynamic-space-size and, where the system allows it,
;;;; under unshare's user, network and IPC namespaces. The child reads the code with the safe
;;;; reader and runs it under the cooperative limits (runner.lisp); every OM.TOOL call that reads
;;;; or writes the image's state is one round trip over the child's pipe, answered by the image
;;;; (service-call), so writes go through the image's locks and curated.Check. A program past its
;;;; time, or a child that crashes, is a process kill: it unwinds nothing in the image.
;;;;
;;;; The wire is one JSON object per line. Child -> image: {"ready": true} once started,
;;;; {"call": name, "args": [value...]} per call, {"done": {...}} last. Image -> child: the
;;;; request {"code", "model", "strings", "timeout", "steps", "heapLimit"} after ready, then
;;;; {"value": v} or {"error": message} per call. Values are tagged (wire-encode): strings and
;;;; null/true as themselves, {"i": "123"} an integer, {"k": "name"} a keyword, {"s": "name"} a
;;;; symbol (read as its lowercase name), {"l": [...]} a list, {"note": [...]}, {"turn": [...]},
;;;; {"memory": [...]} and {"head": [...]} the API's records (a note's freshness verdict is its
;;;; own member, after its merged records, as om.Note.Fresh is).

(in-package "OM.LAYER")

;;; Settings

(defvar *tool-timeout-seconds* 30
  "How long one memory tool program may run (its child's cooperative deadline). The image's own
work answering the program's calls counts toward it.")

(defvar *child-heap-mb* 256 "The child's --dynamic-space-size, in MB.")

(defvar *child-kill-grace* 3
  "Seconds past the program's deadline after which the image kills the child.")

(defvar *child-start-seconds* 30 "How long a child may take to start.")

(defvar *child-command* nil
  "A list of strings that starts a child, or nil: the dumped image with --tool-child
(*image-child* true, set by build-image.lisp), else this runtime and core loading the system.")

(defvar *image-child* nil "True in the dumped image: a child is the image's own executable.")

(defvar *child-unshare* :auto
  "Whether a child runs under unshare -U -r -n -i: :auto (when unshare works here, checked
once), t or nil. OM_TOOL_CHILD_UNSHARE=0 turns it off.")

(defvar *unshare-works* :unknown)

;;; What crosses the pipe

(defparameter +remote-functions+
  '(om.tool:notes om.tool:note-by-id om.tool:history om.tool:recall om.tool:saved om.tool:head
    om.tool:save om.tool:edit om.tool:forget-memory om.tool:feedback
    om.tool:layer om.tool:layer-forget om.tool:layer-rollback om.tool:note-line)
  "The OM.TOOL functions a child forwards to the image: every one that reads the branch, the
curated store or the layer, writes, or dispatches on the model's layer (note-line). The
accessors, show, text and adaptables run in the child.")

(defun remote-function-p (symbol) (member symbol +remote-functions+ :test #'eq))

(defun wire-encode (v)
  (cond ((null v) :null)
        ((eq v t) :true)
        ((stringp v) (coerce v 'simple-string))
        ((integerp v) (om::jobject "i" (princ-to-string v)))
        ((keywordp v) (om::jobject "k" (symbol-name v)))
        ((symbolp v) (om::jobject "s" (string-downcase (symbol-name v))))
        ((note-p v) (om::jobject "note" (vector (note-id v) (note-when v) (note-kind v) (note-subject v) (note-scope v)
                                                (note-relevance v) (note-content v) (note-status v) (note-event-date v)
                                                (note-origin v) (note-outcome v) (note-tool v) (note-target v)
                                                (note-source v) (note-anchor v)
                                                (coerce (note-superseded-by v) 'simple-vector)
                                                (note-tag v) (map 'simple-vector #'wire-encode (note-with v))
                                                (note-fresh v))))
        ((head-view-p v) (om::jobject "head" (vector (head-view-repo-id v) (head-view-head v) (head-view-prefix v)
                                                     (coerce (head-view-files v) 'simple-vector)
                                                     (princ-to-string (head-view-offset v))
                                                     (princ-to-string (head-view-total v))
                                                     (om::jbool (head-view-truncated v)) (head-view-next v))))
        ((turn-p v) (om::jobject "turn" (vector (turn-id v) (turn-when v) (turn-role v) (turn-text v))))
        ((memory-p v) (om::jobject "memory" (vector (mem-name v) (mem-type v) (mem-description v) (mem-body v)
                                                    (mem-project v) (om::jbool (mem-staged v)) (om::jbool (mem-pinned v)))))
        ((consp v)
         (unless (list-length v) (tool-fail "a circular list cannot be passed to the memory tool's API"))
         (unless (null (cdr (last v))) (tool-fail "a dotted list cannot be passed to the memory tool's API"))
         (om::jobject "l" (map 'simple-vector #'wire-encode v)))
        (t (tool-fail "a ~(~A~) cannot be passed to the memory tool's API (strings, integers, keywords, lists and its records can)"
                      (type-of v)))))

(defun wire-decode (v &key (keywords :find))
  "A tagged value as Lisp. keywords :find takes only keywords that exist (the image never
interns a name a child sent), :intern makes them (the child, reading the image's answer)."
  (cond ((or (eq v :null) (eq v :false)) nil)
        ((eq v :true) t)
        ((stringp v) v)
        ((hash-table-p v)
         (let ((k (loop for key being the hash-keys of v return key)))
           (cond ((null k) (error "an empty tagged value"))
                 ((string= k "i") (parse-integer (gethash k v)))
                 ((string= k "k") (let ((name (gethash k v)))
                                    (or (if (eq keywords :intern)
                                            (intern name "KEYWORD")
                                            (find-symbol name "KEYWORD"))
                                        (tool-fail "unknown option :~(~A~)" name))))
                 ((string= k "s") (gethash k v))
                 ((string= k "l") (map 'list (lambda (x) (wire-decode x :keywords keywords)) (gethash k v)))
                 ((string= k "note") (let ((f (gethash k v)))
                                       (make-note :id (aref f 0) :when (aref f 1) :kind (aref f 2) :subject (aref f 3)
                                                  :scope (aref f 4) :relevance (aref f 5) :content (aref f 6)
                                                  :status (aref f 7) :event-date (aref f 8) :origin (aref f 9)
                                                  :outcome (aref f 10) :tool (aref f 11) :target (aref f 12)
                                                  :source (aref f 13) :anchor (aref f 14)
                                                  :superseded-by (coerce (aref f 15) 'list)
                                                  :tag (if (> (length f) 16) (aref f 16) "")
                                                  :with (if (> (length f) 17)
                                                            (map 'list (lambda (x) (wire-decode x :keywords keywords)) (aref f 17))
                                                            '())
                                                  :fresh (if (> (length f) 18) (aref f 18) ""))))
                 ((string= k "head") (let ((f (gethash k v)))
                                       (make-head-view :repo-id (aref f 0) :head (aref f 1) :prefix (aref f 2)
                                                       :files (coerce (aref f 3) 'list)
                                                       :offset (parse-integer (aref f 4)) :total (parse-integer (aref f 5))
                                                       :truncated (eq (aref f 6) :true) :next (aref f 7))))
                 ((string= k "turn") (let ((f (gethash k v)))
                                       (make-turn :id (aref f 0) :when (aref f 1) :role (aref f 2) :text (aref f 3))))
                 ((string= k "memory") (let ((f (gethash k v)))
                                         (make-memory :name (aref f 0) :type (aref f 1) :description (aref f 2)
                                                      :body (aref f 3) :project (aref f 4)
                                                      :staged (eq (aref f 5) :true) :pinned (eq (aref f 6) :true))))
                 (t (error "an unknown tagged value ~A" k)))))
        (t (error "an untagged value"))))

(defun redact-wire (v)
  "An answer's encoded value (wire-encode) with every string redacted (secrets.RedactJSON's rule,
contracts/secrets.md): what the image hands the child is a render of stored or transcript text."
  (cond ((stringp v) (om::redact-secrets v))
        ((hash-table-p v)
         (let ((h (make-hash-table :test 'equal)))
           (maphash (lambda (k x) (setf (gethash k h) (redact-wire x))) v)
           h))
        ((vectorp v) (map 'simple-vector #'redact-wire v))
        (t v)))

(defun write-wire (stream object)
  (write-string (om::json-string object :escape-html nil) stream)
  (write-char #\Newline stream)
  (finish-output stream))

(defun parse-wire (line) (om::json-parse (coerce line 'simple-string)))

;;; The child

(defvar *wire-in* nil "The child's pipe from the image.")
(defvar *wire-out* nil "The child's pipe to the image.")
(defvar *strings* (make-hash-table :test 'equal) "The call's strings argument, by name.")

(defun om.tool:text (name)
  "The text the call's strings argument holds under name."
  (multiple-value-bind (v ok) (gethash (if (symbolp name) (string-downcase (symbol-name name)) name) *strings*)
    (unless ok
      (tool-fail "no string named ~A in the call's strings argument~@[ (it holds ~{~A~^, ~})~]"
                 (q (princ-to-string name))
                 (sort (loop for k being the hash-keys of *strings* collect k) #'string<)))
    v))

(defun remote-invoke (name args)
  "One round trip to the image: the call's answer, or the image's error as a tool-error."
  (unless *wire-out* (tool-fail "~A needs the image, and this child has no pipe to it" name))
  (let ((encoded (map 'simple-vector #'wire-encode args)))
    (write-wire *wire-out* (om::jobject "call" name "args" encoded)))
  (let ((line (read-line *wire-in* nil nil)))
    (unless line (sb-ext:exit :code 4 :abort t))
    (let ((answer (parse-wire line)))
      (multiple-value-bind (err has-error) (gethash "error" answer)
        (when has-error (tool-fail "~A" err)))
      (wire-decode (om::jget answer "value") :keywords :intern))))

(defun go-looking-p (code)
  "Whether code looks like Go (the Go tool's calls, assignments or declarations)."
  (or (search "fmt." code) (search ":=" code) (search "func " code)
      (loop for i = (search "om." code) then (search "om." code :start2 (1+ i))
            while i thereis (and (< (+ i 3) (length code)) (upper-case-p (char code (+ i 3)))))))

(defparameter +lisp-hint+
  "this memory tool runs Common Lisp, not Go: write (notes :search \"<words>\"), (recall \"<id>\") or (history :search \"<words>\"), print what you want shown, and send empty code for the manual"
  "What a refusal of Go-looking code adds.")

(defun child-run (req)
  "Runs one program in this child: (values status text error) with status \"ok\", \"compile\"
(refused when read: nothing ran) or \"runtime\" (an error, or a limit the program reached)."
  (let* ((code (om::str (om::jget req "code")))
         (model (om::str (om::jget req "model")))
         (lisp code) (note nil))
    (when (go-snippet-p code)
      (handler-case (setf lisp (go-snippet-lisp code)
                          note (format nil ";; ~A runs here as ~A" (string-trim '(#\Space #\Tab #\Newline #\Return) code) lisp))
        (snippet-error (e)
          (return-from child-run
            (values "compile" "" (format nil "~A; ~A" e +lisp-hint+))))))
    (let ((forms (handler-case (safe-read-all lisp (model-package model))
                   (refused (e)
                     (return-from child-run
                       (values "compile" ""
                               (if (go-looking-p code) (format nil "~A (~A)" e +lisp-hint+) (princ-to-string e))))))))
      (dolist (f forms)
        (let ((p (form-placeholder (car f))))
          (when p (return-from child-run (values "compile" "" (format nil "nothing ran: ~A" (placeholder-error p))))))))
    (let* ((r (run-tool-code lisp :call (make-call :model model) :timeout *timeout-seconds*))
           (text (if note
                     (format nil "~A~%~A" note (tool-result-text r))
                     (tool-result-text r))))
      (if (tool-result-error r)
          (values "runtime" text (if (go-looking-p code)
                                     (format nil "~A (~A)" (tool-result-error r) +lisp-hint+)
                                     (tool-result-error r)))
          (values "ok" text nil)))))

(defun tool-child-main ()
  "The child's toplevel (om-engine --tool-child, or --eval from source): ready, one request,
its calls, done; then exit."
  (sb-ext:disable-debugger)
  (let ((in (sb-sys:make-fd-stream 0 :input t :buffering :full :external-format :utf-8))
        (out (sb-sys:make-fd-stream 1 :output t :buffering :full :external-format :utf-8)))
    (setf *wire-in* in *wire-out* out *remote* t)
    (let ((code 0))
      (handler-case
          (progn
            (write-wire out (om::jobject "ready" :true))
            (let* ((line (or (read-line in nil nil) (sb-ext:exit :code 4 :abort t)))
                   (req (parse-wire line)))
              (let ((strings (om::jget req "strings")))
                (when (hash-table-p strings)
                  (maphash (lambda (k v) (when (stringp v) (setf (gethash k *strings*) v))) strings)))
              (let ((n (om::jget req "timeout"))) (when (realp n) (setf *timeout-seconds* (if (= n (round n)) (round n) n))))
              (let ((n (om::jget req "steps"))) (when (realp n) (setf *fuel-steps* (round n))))
              ;; The heap check stops a program while the child still has room: half of what is
              ;; left of its dynamic space once loaded, unless the image asked for another.
              (setf *heap-limit-bytes*
                    (let ((n (om::jget req "heapLimit")))
                      (if (realp n) (round n) (floor (- (sb-ext:dynamic-space-size) (heap-usage)) 2))))
              (multiple-value-bind (status text err) (child-run req)
                (write-wire out (om::jobject "done" (om::jobject "status" status "text" text "error" (or err :null)))))))
        (error (e)
          (setf code 1)
          (ignore-errors (write-wire out (om::jobject "done" (om::jobject "status" "crashed" "text" ""
                                                                         "error" (princ-to-string e)))))))
      (finish-output out)
      (sb-ext:exit :code code :abort t))))

;;; The image's side: starting a child

(defun unshare-works-p ()
  (when (eq *unshare-works* :unknown)
    (setf *unshare-works*
          (and (probe-file "/usr/bin/unshare")
               (handler-case
                   (let ((p (sb-ext:run-program "/usr/bin/unshare" '("-U" "-r" "-n" "-i" "/bin/true")
                                                :input nil :output nil :error nil :wait t)))
                     (eql (sb-ext:process-exit-code p) 0))
                 (error () nil)))))
  *unshare-works*)

(defun use-unshare-p ()
  (and (not (equal (sb-posix:getenv "OM_TOOL_CHILD_UNSHARE") "0"))
       (case *child-unshare* ((nil) nil) ((t) t) (t (unshare-works-p)))))

(defun child-argv ()
  "The child's program and arguments."
  (let ((heap (format nil "~DMB" *child-heap-mb*)))
    (let ((cmd (or *child-command*
                   (if *image-child*
                       (list (namestring sb-ext:*runtime-pathname*) "--dynamic-space-size" heap "--tool-child")
                       (list (namestring sb-ext:*runtime-pathname*) "--core" (namestring sb-ext:*core-pathname*)
                             "--dynamic-space-size" heap "--noinform" "--disable-ldb" "--lose-on-corruption"
                             "--end-runtime-options" "--non-interactive" "--no-sysinit" "--no-userinit"
                             "--load" (namestring (asdf:system-relative-pathname "om-engine" "load.lisp"))
                             "--eval" "(om.layer::tool-child-main)")))))
      (if (and (null *child-command*) (use-unshare-p))
          (list* "/usr/bin/unshare" "-U" "-r" "-n" "-i" "--" cmd)
          cmd))))

(defun child-environment ()
  (append (list "LANG=C.UTF-8")
          (loop for name in '("HOME" "PATH" "SBCL_HOME" "XDG_CACHE_HOME" "TMPDIR")
                for v = (sb-posix:getenv name)
                when v collect (format nil "~A=~A" name v))))

(defstruct (child (:constructor %make-child))
  process (lines (sb-concurrency:make-mailbox :name "om tool child lines")) reader drainer
  (stderr (make-string-output-stream)) (stderr-lock (sb-thread:make-mutex :name "om tool child stderr")))

(defun start-child ()
  (let* ((argv (child-argv))
         (p (sb-ext:run-program (first argv) (rest argv) :input :stream :output :stream :error :stream
                                :wait nil :environment (child-environment) :external-format :utf-8
                                :search nil))
         (c (%make-child :process p)))
    (setf (child-reader c)
          (sb-thread:make-thread
           (lambda ()
             (handler-case
                 (loop for line = (read-line (sb-ext:process-output p) nil nil)
                       while line do (sb-concurrency:send-message (child-lines c) line))
               (error () nil))
             (sb-concurrency:send-message (child-lines c) :eof))
           :name "om tool child reader")
          (child-drainer c)
          (sb-thread:make-thread
           (lambda ()
             (handler-case
                 (loop with s = (sb-ext:process-error p)
                       for ch = (read-char s nil nil)
                       while ch do (sb-thread:with-mutex ((child-stderr-lock c))
                                     (write-char ch (child-stderr c))))
               (error () nil)))
           :name "om tool child stderr"))
    c))

(defun child-stderr-tail (c)
  (let ((all (sb-thread:with-mutex ((child-stderr-lock c))
               (let ((s (get-output-stream-string (child-stderr c))))
                 (write-string s (child-stderr c))
                 s))))
    (string-trim '(#\Space #\Newline #\Return #\Tab)
                 (if (> (length all) 600) (subseq all (- (length all) 600)) all))))

(defun stop-child (c &key kill)
  "Ends the child: killed (SIGKILL) when kill, else waited for; its streams closed and its
threads joined, so nothing of it is left."
  (let ((p (child-process c)))
    (when (and kill (sb-ext:process-alive-p p))
      (ignore-errors (sb-ext:process-kill p 9)))
    (ignore-errors (close (sb-ext:process-input p)))
    ;; A child that said done exits at once; one that has not after 5 s is killed, so this
    ;; never waits without a bound.
    (loop repeat 100 while (ignore-errors (sb-ext:process-alive-p p)) do (sleep 0.05))
    (when (ignore-errors (sb-ext:process-alive-p p))
      (ignore-errors (sb-ext:process-kill p 9)))
    (ignore-errors (sb-ext:process-wait p))
    (ignore-errors (sb-thread:join-thread (child-reader c) :timeout 5 :default nil))
    (ignore-errors (sb-thread:join-thread (child-drainer c) :timeout 5 :default nil))
    (ignore-errors (sb-ext:process-close p))
    (ignore-errors (sb-ext:process-exit-code p))))

;;; The image's side: one call's record (omhost.Record)

(defstruct (tool-record (:conc-name rec-))
  (writes '()) (hits '()) (recalled '()) (searches '()) (reads 0) (found 0) (calls 0))

(defun rec-read (rec hits found searches)
  (incf (rec-reads rec))
  (when found (incf (rec-found rec)))
  (dolist (h hits) (unless (member h (rec-hits rec) :test #'string=) (setf (rec-hits rec) (append (rec-hits rec) (list h)))))
  (setf (rec-searches rec) (append (rec-searches rec) searches)))

(defun rec-write (rec op name &key type description)
  (setf (rec-writes rec) (append (rec-writes rec) (list (list op name type description)))))

(defun rec-empty-p (rec) (and (plusp (rec-reads rec)) (zerop (rec-found rec))))

(defun key-arg (args key)
  (loop for (k v) on args when (eq k key) return v))

(defun search-strings (v)
  (cond ((null v) '()) ((stringp v) (list v)) ((listp v) (remove-if-not #'stringp v)) (t '())))

(defun service-call (name args rec)
  "Answers one call of a child for *call*: (values the answer, nil) or (values nil the error's
text), recording it as omhost's Record does."
  (incf (rec-calls rec))
  (handler-case
      (cond
        ((string= name "define")
         (let* ((model (model-of-call)) (d (handler-case (layer-hold model (first args))
                                             (refused (e) (tool-fail "~A" (refused-reason e))))))
           (rec-write rec "layer-hold" (format nil "~A/~A" +kind-adaptable+ (ldef-name d)))
           (values (held-message d model) nil)))
        (t
         (let ((symbol (find-symbol (string-upcase name) "OM.TOOL")))
           (unless (and symbol (remote-function-p symbol))
             (tool-fail "~A is not a call the image answers" name))
           (when (string= name "recall")
             (setf (rec-recalled rec) (append (rec-recalled rec) (list (name-of (first args))))))
           (let ((value (apply (fdefinition symbol) args)))
             (cond ((member name '("notes") :test #'string=)
                    (rec-read rec (mapcar #'note-id value) (and value t) (search-strings (key-arg args :search))))
                   ((string= name "history")
                    (rec-read rec '() (and value t) (search-strings (key-arg args :search))))
                   ((string= name "note-by-id") (rec-read rec (list (note-id value)) t '()))
                   ((string= name "recall") (rec-read rec (list (name-of (first args))) t '()))
                   ((string= name "saved") (rec-read rec (mapcar #'mem-name value) (and value t) '()))
                   ((string= name "layer") (rec-read rec '() (and value t) '()))
                   ((string= name "save")
                    (rec-write rec "save" (first args) :type (second args) :description (third args)))
                   ((string= name "edit")
                    (rec-write rec "edit" (first args) :type (key-arg (rest args) :type)
                                                       :description (key-arg (rest args) :description)))
                   ((string= name "forget-memory") (rec-write rec "forget" (first args)))
                   ((string= name "feedback") (rec-write rec "feedback" (first args)))
                   ((string= name "layer-forget")
                    (rec-write rec "layer-forget" (format nil "~A/~A" +kind-adaptable+ (name-of (first args)))))
                   ((string= name "layer-rollback") (rec-write rec "layer-rollback" (princ-to-string (first args)))))
             (values value nil)))))
    (tool-error (e) (values nil (tool-error-message e)))
    (refused (e) (values nil (refused-reason e)))
    (error (e) (values nil (handler-case (princ-to-string e) (error () "an error in the image"))))))

;;; The image's side: running one program

(defstruct (child-result (:conc-name cr-))
  (status "ok") (text "") (error nil) (record (make-tool-record)))

(defun run-tool-child (code &key (call *call*) strings (timeout *tool-timeout-seconds*) kill-after
                              steps heap-limit (cancelled-p (lambda () nil)))
  "Runs one memory tool program in a child: a child-result whose status is \"ok\",
\"compile\", \"runtime\", \"crashed\" (the child died, or was killed past kill-after seconds,
by default timeout plus *child-kill-grace*) or \"cancelled\" (cancelled-p turned true: killed)."
  (let* ((rec (make-tool-record))
         (kill-after (or kill-after (+ timeout *child-kill-grace*)))
         (c (handler-case (start-child)
              (error (e)
                (return-from run-tool-child
                  (make-child-result :status "crashed" :record rec
                                     :error (format nil "the memory tool's child process did not start: ~A" e))))))
         (input (sb-ext:process-input (child-process c)))
         (result nil) (killed nil))
    (flet ((finish (status err &optional (text ""))
             (setf result (make-child-result :status status :text text :error err :record rec)))
           (next (deadline)
             ;; The child's next line before deadline (internal time), :eof, or :timeout.
             (loop
               (let ((left (/ (- deadline (get-internal-real-time)) internal-time-units-per-second)))
                 (when (funcall cancelled-p) (return :cancelled))
                 (when (<= left 0) (return :timeout))
                 (multiple-value-bind (m ok) (sb-concurrency:receive-message (child-lines c) :timeout (min left 0.05))
                   (when ok (return m)))))))
      (unwind-protect
           (with-call (call)
             ;; ready: lines before it (a load's own output from source) are not the wire's
             (let ((start-deadline (+ (get-internal-real-time) (* *child-start-seconds* internal-time-units-per-second))))
               (loop
                 (let ((m (next start-deadline)))
                   (cond ((eq m :eof) (finish "crashed" (format nil "the memory tool's child process ended before it started~@[: ~A~]"
                                                               (let ((s (child-stderr-tail c))) (and (string/= s "") s))))
                                      (return))
                         ((eq m :cancelled) (setf killed t) (finish "cancelled" "cancelled before the program started") (return))
                         ((eq m :timeout) (setf killed t) (finish "crashed" "the memory tool's child process did not start in time") (return))
                         ((let ((o (ignore-errors (parse-wire m)))) (and (hash-table-p o) (eq (om::jget o "ready") :true)))
                          (return))))))
             (unless result
               (write-wire input (om::jobject "code" code "model" (call-model call)
                                              "strings" (or strings (om::jobject)) "timeout" timeout
                                              "steps" (or steps :null) "heapLimit" (or heap-limit :null)))
               (let ((deadline (+ (get-internal-real-time) (round (* kill-after internal-time-units-per-second)))))
                 (loop
                   (let ((m (next deadline)))
                     (cond ((eq m :timeout)
                            (setf killed t)
                            (finish "crashed" (format nil "the program ran past ~As and its process was killed" kill-after))
                            (return))
                           ((eq m :cancelled)
                            (setf killed t)
                            (finish "cancelled" "the call was cancelled and the program's process was killed")
                            (return))
                           ((eq m :eof)
                            (loop repeat 100 while (sb-ext:process-alive-p (child-process c)) do (sleep 0.05))
                            (setf killed (sb-ext:process-alive-p (child-process c)))
                            (let ((tail (child-stderr-tail c)))
                              (finish "crashed" (format nil "the program's process ended (exit ~A)~@[: ~A~]"
                                                        (sb-ext:process-exit-code (child-process c))
                                                        (and (string/= tail "") (last-line tail)))))
                            (return))
                           (t
                            (let ((o (handler-case (parse-wire m) (error () nil))))
                              (cond ((not (hash-table-p o))
                                     (setf killed t)
                                     (finish "crashed" "the program's process wrote something that is not the memory tool's wire")
                                     (return))
                                    ((om::jget o "done")
                                     (let ((d (om::jget o "done")))
                                       (finish (om::str (om::jget d "status"))
                                               (let ((e (om::jget d "error"))) (and (stringp e) e))
                                               (om::str (om::jget d "text"))))
                                     (return))
                                    ((stringp (om::jget o "call"))
                                     (multiple-value-bind (value err)
                                         (handler-case
                                             (service-call (om::jget o "call")
                                                           (map 'list #'wire-decode (let ((a (om::jget o "args"))) (if (vectorp a) a #())))
                                                           rec)
                                           (error (e) (values nil (princ-to-string e))))
                                       (handler-case
                                           (write-wire input (if err
                                                                 (om::jobject "error" err)
                                                                 (om::jobject "value" (handler-case (redact-wire (wire-encode value))
                                                                                        (error (e) (setf err (princ-to-string e)) :null)))))
                                         (error () nil))))
                                    (t (setf killed t)
                                       (finish "crashed" "the program's process wrote something that is not the memory tool's wire")
                                       (return)))))))))))
        (stop-child c :kill (or killed (null result) (string/= (cr-status result) "ok")))))
    result))

(defun last-line (s)
  (let ((i (position #\Newline s :from-end t)))
    (if i (subseq s (1+ i)) s)))
