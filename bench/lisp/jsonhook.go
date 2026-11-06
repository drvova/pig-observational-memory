// jsonhook is hook.lisp's Go twin for docs/lisp-measurements.md: it reads a JSONL transcript,
// decodes every line with encoding/json into an any, appends one line to a file under an
// exclusive flock(2) on <file>.lock (as memory/filelock does) and exits. `jsonhook --hello`
// prints hello, the Go side of the startup comparison. With JSONHOOK_HWM=1 in the environment it
// prints its own peak RSS (VmHWM) on stderr at exit, since a child spawned from bench.py cannot
// report a peak below the spawner's (rusage keeps the pre-exec high-water mark).
//
// Build it outside the workspace: GOWORK=off go build -o jsonhook jsonhook.go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
)

func hwm() {
	if os.Getenv("JSONHOOK_HWM") == "" {
		return
	}
	data, _ := os.ReadFile("/proc/self/status")
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "VmHWM:") {
			fmt.Fprintln(os.Stderr, strings.TrimSpace(l))
		}
	}
}

func count(v any) int {
	n := 1
	switch x := v.(type) {
	case map[string]any:
		for _, e := range x {
			n += count(e)
		}
	case []any:
		for _, e := range x {
			n += count(e)
		}
	}
	return n
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--hello" {
		fmt.Println("hello")
		hwm()
		return
	}
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: jsonhook TRANSCRIPT OUT | jsonhook --hello")
		os.Exit(2)
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	lines, values := 0, 0
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		lines++
		values += count(v)
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out := os.Args[2]
	lock, err := os.OpenFile(out+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	w, err := os.OpenFile(out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = fmt.Fprintf(w, "{\"hook\":\"stop\",\"lines\":%d,\"values\":%d}\n", lines, values)
		w.Close()
	}
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	lock.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%d lines %d values\n", lines, values)
	hwm()
}
