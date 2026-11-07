;;;; Appending to an append-only JSON-lines file (memory/jsonl Append): every file the image
;;;; appends to, the layer's manifest and units, the usage ledger, the curated usage, the floor
;;;; record and the neutral store, takes one line here, under the lock its caller already holds.
;;;;
;;;; A crash can leave a file's last line torn: the bytes of a write cut off, with no newline.
;;;; Every reader (Go's and the image's) skips a line that does not decode, so the fragment is
;;;; harmless as it stands; appending after it as it stands would glue the next line onto it, and
;;;; both would be lost. So an append ends a torn tail with a newline first: the fragment stays one
;;;; line no reader decodes, and a last line that does decode without its newline (a complete
;;;; object) stays the line every reader already read. Nothing is truncated, so no append changes
;;;; what a reader read before it. The line goes in one write(2), then fsync, and the directory is
;;;; fsynced when the append created the file, so a line an append returned is durable: a reader
;;;; can never read a line a crash later takes back.

(in-package "OM")

(eval-when (:compile-toplevel :load-toplevel :execute)
  (require :sb-posix))

(defun fd-write-all (fd octets)
  (let ((n (length octets)) (done 0))
    (sb-sys:with-pinned-objects (octets)
      (loop while (< done n)
            do (let ((w (sb-posix:write fd (sb-sys:sap+ (sb-sys:vector-sap octets) done) (- n done))))
                 (when (<= w 0) (error "write: ~D of ~D bytes" done n))
                 (incf done w))))))

(defun fd-last-byte (fd size)
  "The byte at offset size - 1 of the open file fd."
  (let ((buf (make-array 1 :element-type '(unsigned-byte 8))))
    (sb-posix:lseek fd (1- size) sb-posix:seek-set)
    (sb-sys:with-pinned-objects (buf)
      (and (= (sb-posix:read fd (sb-sys:vector-sap buf) 1) 1) (aref buf 0)))))

(defun fsync-dir (dir)
  "fsync(2) of a directory, so an entry created in it is durable; an error is ignored (some
file systems refuse it)."
  (handler-case
      (let ((fd (sb-posix:open (if (string= dir "") "." dir) sb-posix:o-rdonly)))
        (unwind-protect (ignore-errors (sb-posix:fsync fd)) (sb-posix:close fd)))
    (error () nil)))

(defun torn-tail-p (path)
  "Whether path's last byte is not a newline (a non-empty file a write was cut off in)."
  (handler-case
      (let ((fd (sb-posix:open path sb-posix:o-rdonly)))
        (unwind-protect
             (let ((size (sb-posix:stat-size (sb-posix:fstat fd))))
               (and (plusp size) (not (eql (fd-last-byte fd size) 10))))
          (sb-posix:close fd)))
    (sb-posix:syscall-error () nil)))

(defun append-jsonl (path line &key (mode #o600))
  "Appends line and a newline to path (created with mode), the caller holding its lock: a torn
tail ended with a newline first, one write, fsync, and the directory fsynced on creation.
Returns t when the append found a torn tail."
  (let* ((created (not (probe-file path)))
         (fd (sb-posix:open path (logior sb-posix:o-rdwr sb-posix:o-creat sb-posix:o-append) mode))
         (torn nil))
    (unwind-protect
         (let ((size (sb-posix:stat-size (sb-posix:fstat fd))))
           (setf torn (and (plusp size) (not (eql (fd-last-byte fd size) 10))))
           (fd-write-all fd (utf8-octets (if torn
                                             (concatenate 'string (string #\Newline) line (string #\Newline))
                                             (concatenate 'string line (string #\Newline)))))
           (sb-posix:fsync fd))
      (sb-posix:close fd))
    (when created (fsync-dir (directory-namestring path)))
    torn))
