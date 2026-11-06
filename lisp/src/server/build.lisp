;;;; W6: what initialize reports so a client can refuse a stale image (docs/lisp-port-peer.md):
;;;; the build id, the SHA-256 of the image's sources (every lisp/src file, by path) and of the
;;;; memory tool's spec, computed when the system loads, so a dumped image carries the id of the
;;;; sources it was built from (lisp/build-image.lisp also writes it beside the image); and the
;;;; memory tool's spec (toolspec.json, generated from Go's memory/omtool by
;;;; `cd memory && go test ./enginerpc -run LispToolSpec -args -write`).

(in-package "OM.SERVER")

(defun source-root ()
  (asdf:system-source-directory "om-engine"))

(defun read-octets (path)
  (with-open-file (in path :element-type '(unsigned-byte 8))
    (let ((buf (make-array (file-length in) :element-type '(unsigned-byte 8))))
      (read-sequence buf in)
      buf)))

(defun compute-build-id ()
  "The first 16 hex digits of the SHA-256 over each source file's path (relative to lisp/), a
NUL, its bytes and a NUL, the files sorted by path."
  (let* ((root (source-root))
         (files (sort (mapcar (lambda (p) (enough-namestring p root))
                              (append (directory (merge-pathnames "src/**/*.lisp" root))
                                      (directory (merge-pathnames "src/**/*.json" root))))
                      #'string<))
         (parts '()))
    (dolist (f files)
      (push (om::utf8-octets f) parts)
      (push (make-array 1 :element-type '(unsigned-byte 8) :initial-element 0) parts)
      (push (read-octets (merge-pathnames f root)) parts)
      (push (make-array 1 :element-type '(unsigned-byte 8) :initial-element 0) parts))
    (let* ((total (reduce #'+ parts :key #'length))
           (all (make-array total :element-type '(unsigned-byte 8)))
           (at 0))
      (dolist (p (nreverse parts))
        (replace all p :start1 at)
        (incf at (length p)))
      (subseq (om::sha256-octets all) 0 16))))

(defun read-tool-spec ()
  (string-trim '(#\Newline #\Space)
               (sb-ext:octets-to-string (read-octets (merge-pathnames "src/server/toolspec.json" (source-root)))
                                        :external-format :utf-8)))

(defparameter *tool-spec-json* (read-tool-spec)
  "The memory tool's spec as initialize answers it (enginerpc.ToolSpec's JSON).")

(defparameter *build-id* (compute-build-id)
  "The image's build id: the sources it was loaded from.")
