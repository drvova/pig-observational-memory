;;;; memory/curated held.go: curated memories that weaken security are held (docs/brain-memory.md,
;;;; decision 19): kept and listed in /om:status, left out of the index, saved and the level-1
;;;; input, never promoted, except in a session whose user's words restate them (a released
;;;; usage line). Loaded after curated-index.lisp.

(in-package "OM.LAYER")

(defun memory-held-rule (m)
  "curated.HeldRule: the insecure rule m's description or body matches, nil for none."
  (om::insecure-match (concatenate 'string (mem-description m) (string #\Newline) (mem-body m))))

(defun released-p (m events session)
  "curated.Released: session's user words restated the held memory m since it was last saved or
edited."
  (and (string/= session "")
       (some (lambda (e) (and (string= (ul-event e) "released") (string= (ul-session e) session)
                              (usage-event-time e)
                              (or (null (mem-updated m)) (not (time-before-p (usage-event-time e) (mem-updated m))))))
             events)))

(defun shown-as (dir memories session)
  "Store.Shown: memories without the held ones session did not release, in order."
  (let ((events (handler-case (usage-events-by-name dir) (error () (make-hash-table :test 'equal)))))
    (remove-if (lambda (m) (and (memory-held-rule m) (not (released-p m (gethash (mem-name m) events) session))))
               memories)))

(defun restates-p (quote)
  "curated.restates: a user's quote that itself matches the pattern list."
  (and (om::insecure-match quote) t))

(defun release-memory (dir name session quote)
  "Store.release: the released usage line."
  (append-usage dir name "released" :quote quote :user t :session session))

(defun held-note (rule)
  (format nil "It weakens security (~A), so it is held: listed in /om:status, kept out of the memory index and saved, and never applied, until a user's own words restate it in a session." rule))

(defun released-note (rule)
  (format nil "It weakens security (~A), so it is held: the user's words restate it, so it is shown in this session only; every other session holds it until its user's words restate it." rule))

(defun save-held-text (c m user-quote)
  "Call.hold: what a save says of a held memory, \"\" for one that is not held."
  (let ((rule (memory-held-rule m)))
    (cond ((null rule) "")
          ((and (string/= user-quote "") (restates-p user-quote))
           (release-memory (cc-dir c) (mem-name m) (cc-source c) user-quote)
           (concatenate 'string " " (released-note rule)))
          (t (concatenate 'string " " (held-note rule))))))

(defun held-memories (dir project)
  "Store.Held: (name type description rule) of each held memory that applies in project, staged
ones included, in name order."
  (loop for m in (list-memories dir project)
        for rule = (memory-held-rule m)
        when rule collect (list (mem-name m) (mem-type m) (mem-description m) rule)))
