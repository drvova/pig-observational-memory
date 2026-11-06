;;;; Dumps the om engine image, an executable whose toplevel is om.server:main. Run it through
;;;; lisp/build-image.sh, which starts SBCL with an explicit --dynamic-space-size: the image
;;;; saves its runtime options, so it starts with that heap and passes every argument to main.
;;;;   OM_IMAGE_OUTPUT=<path> sbcl --dynamic-space-size 512MB --non-interactive --load lisp/build-image.lisp

(load (merge-pathnames "load.lisp" (make-pathname :name nil :type nil :defaults *load-truename*)))

(let ((output (sb-posix:getenv "OM_IMAGE_OUTPUT"))
      (compress (equal (sb-posix:getenv "OM_IMAGE_COMPRESS") "1")))
  (unless (and output (string/= output ""))
    (format *error-output* "build-image: set OM_IMAGE_OUTPUT to the image's path~%")
    (sb-ext:exit :code 2))
  (when (and compress (not (member :sb-core-compression *features*)))
    (format *error-output* "build-image: this SBCL has no core compression~%")
    (sb-ext:exit :code 2))
  (format t "build-image: ~A (heap ~D MB~:[~;, compressed~])~%" output
          (floor (sb-ext:dynamic-space-size) (* 1024 1024)) compress)
  (finish-output)
  ;; The build id beside the image (<output>.build), which a client compares with initialize's
  ;; answer to refuse a stale image (memory/lispimage ExpectedBuild).
  (with-open-file (out (concatenate 'string output ".build") :direction :output :if-exists :supersede)
    (write-line om.server::*build-id* out))
  ;; A memory tool child is this executable run with --tool-child (docs/lisp-core.md, decision 9).
  (setf om.layer::*image-child* t)
  (sb-ext:save-lisp-and-die output :executable t :toplevel #'om.server:main
                                   :save-runtime-options t :compression compress))
