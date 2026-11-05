;;;; JSON as encoding/json decodes into any and encodes back, SHA-256, and timestamps.

(in-package "OM")

(deftest json-go-encoding
  (check (json-string (json-parse "{\"b\":[1,2.5,1e21,1.5e-7,true,null],\"a\":\"x<y&z\"}"))
         "{\"a\":\"x\\u003cy\\u0026z\",\"b\":[1,2.5,1e+21,1.5e-7,true,null]}")
  (check (json-stringify (json-parse "{\"cmd\":\"a && b > c\"}")) "{\"cmd\":\"a && b > c\"}")
  (check (json-stringify nil) "{}")
  (check (json-stringify :null) "{}")
  (check (go-float-string 1791452262553d0) "1791452262553")
  (check (go-float-string 0.1d0) "0.1")
  (check (go-float-string 123456789012345678901234d0) "1.2345678901234569e+23")
  (check (go-float-string -0d0) "-0")
  ;; Escapes: \b and \f by name (Go 1.22 on), other controls as \u00XX, U+2028 always.
  (check (json-string (format nil "a~Cb~Cc~C" (code-char 8) (code-char 1) (code-char #x2028)))
         "\"a\\bb\\u0001c\\u2028\""))

(deftest json-decoding
  (check (json-string (json-parse "\"\\ud83d\\ude00 \\ud800x\"")) (format nil "\"~C ~Cx\"" (code-char #x1f600) (code-char #xfffd)))
  (check (handler-case (progn (json-parse-object "[1]") :ok) (json-error () :error)) :error)
  (check (handler-case (progn (json-parse-object "{\"a\":1} x") :ok) (json-error () :error)) :error)
  (check (json-parse-object "null") nil)
  (check (jget (json-parse-object "{\"a\":1,\"a\":2}") "a") 2d0))

(deftest sha256-vectors
  (check (sha256-octets (utf8-octets "")) "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
  (check (sha256-octets (utf8-octets "abc")) "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
  (check (sha256-octets (utf8-octets (make-string 1000 :initial-element #\a)))
         "41edece42d63e8d9bf515a9ba6932e1c20cbc9f5a5d134645adb5db1b9737ea3")
  (check (hash-id "abc") "ba7816bf8f01"))

(deftest timestamps
  (check (format-timestamp "2026-09-24T08:01:00.123Z") "2026-09-24 08:01")
  (check (format-timestamp "2026-09-24T08:01:00+02:00") "2026-09-24 06:01")
  (check (format-timestamp "2026-09-24T08:01:00.5-0130") "2026-09-24 09:31")
  (check (format-timestamp "2026-09-24") "2026-09-24 00:00")
  (check (format-timestamp "2026-09-24 08:01") "2026-09-24 08:01")
  (check (format-timestamp 1791452262553d0) "2026-10-08 09:37")
  (check (format-timestamp "2026-02-30T08:01:00Z") "????-??-?? ??:??")
  (check (format-timestamp nil) "????-??-?? ??:??")
  (check (millis "2026-10-08T09:37:42.553Z") 1791452262553)
  (check (millis "2026-10-08") nil)
  (check (format-day (days-from-civil 2024 3 1)) "2024-03-01")
  (check (format-day (1- (days-from-civil 2024 3 1))) "2024-02-29")
  (check (weekday-of-days (days-from-civil 2023 5 24)) 3))
