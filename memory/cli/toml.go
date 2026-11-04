package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// TOMLSpec is a harness whose MCP servers are TOML tables [<Table>.<name>]: om writes its table
// at the end of the file, every line marked as om's (managedMark), and uninstall takes out
// exactly those lines, as for Codex.
type TOMLSpec struct {
	Path  map[string]string
	Table string
	Entry func(exe, harness string) map[string]any
}

func installTOML(w writer, exe, harness string, spec *TOMLSpec, remove bool) error {
	path := pathFor(spec.Path)
	if path == "" {
		fmt.Fprintf(w.out, "%s: no config path on %s\n", harness, runtime.GOOS)
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if !strings.HasSuffix(line, managedMark) && (line != "" || len(kept) > 0) {
			kept = append(kept, line)
		}
	}
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if remove {
		if body == "" {
			return w.write(path, nil)
		}
		return w.write(path, []byte(body+"\n"))
	}
	entry := spec.Entry(exe, harness)
	keys := make([]string, 0, len(entry))
	for k := range entry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	table := []string{"[" + spec.Table + ".om] " + managedMark}
	for _, k := range keys {
		v, err := tomlValue(entry[k])
		if err != nil {
			return fmt.Errorf("%s: %s: %w", harness, k, err)
		}
		table = append(table, k+" = "+v+" "+managedMark)
	}
	out := ""
	if body != "" {
		out = body + "\n\n"
	}
	return w.write(path, []byte(out+strings.Join(table, "\n")+"\n"))
}

// tomlValue is a string, bool, number or list of those as TOML.
func tomlValue(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x), nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(x), nil
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			s, err := tomlValue(e)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case map[string]any:
		if len(x) == 0 {
			return "{}", nil
		}
	}
	return "", fmt.Errorf("value %v has no TOML form here", v)
}
