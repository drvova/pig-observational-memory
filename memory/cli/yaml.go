package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"

	"gopkg.in/yaml.v3"
)

// YAMLSpec is a harness whose MCP servers live in a YAML file: under Key as a map from name to
// server (Goose's extensions), or, with List, as a list of servers each carrying its name
// (Continue's mcpServers). The file is edited at the node level, so its comments and order stay.
type YAMLSpec struct {
	Path  map[string]string
	Key   string
	List  bool
	Entry func(exe, harness string) map[string]any
	// Skeleton are the top-level keys a new file needs (Continue's name, version and schema).
	Skeleton map[string]any
}

func installYAML(w writer, exe, harness string, spec *YAMLSpec, remove bool) error {
	path := pathFor(spec.Path)
	if path == "" {
		fmt.Fprintf(w.out, "%s: no config path on %s\n", harness, runtime.GOOS)
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s is not YAML om can edit: %w", path, err)
		}
	}
	if doc.Kind == 0 {
		if remove {
			return nil
		}
		root := &yaml.Node{Kind: yaml.MappingNode}
		for k, v := range spec.Skeleton {
			setKey(root, k, value(v))
		}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: the top level is not a map", path)
	}
	servers := getKey(root, spec.Key)
	if spec.List {
		if servers == nil || servers.Kind != yaml.SequenceNode {
			servers = &yaml.Node{Kind: yaml.SequenceNode}
		}
		kept := servers.Content[:0]
		for _, item := range servers.Content {
			if n := getKey(item, "name"); n == nil || n.Value != "om" {
				kept = append(kept, item)
			}
		}
		servers.Content = kept
		if !remove {
			item := value(spec.Entry(exe, harness))
			// The item's name leads it, as the harness's own examples write it.
			item.Content = append([]*yaml.Node{{Kind: yaml.ScalarNode, Value: "name"}, {Kind: yaml.ScalarNode, Value: "om"}}, item.Content...)
			servers.Content = append(servers.Content, item)
		}
		if len(servers.Content) == 0 {
			deleteKey(root, spec.Key)
		} else {
			setKey(root, spec.Key, servers)
		}
	} else {
		if servers == nil || servers.Kind != yaml.MappingNode {
			servers = &yaml.Node{Kind: yaml.MappingNode}
		}
		deleteKey(servers, "om")
		if !remove {
			setKey(servers, "om", value(spec.Entry(exe, harness)))
		}
		if len(servers.Content) == 0 {
			deleteKey(root, spec.Key)
		} else {
			setKey(root, spec.Key, servers)
		}
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	return w.write(path, out.Bytes())
}

// value is v as a YAML node.
func value(v any) *yaml.Node {
	var n yaml.Node
	_ = n.Encode(v)
	return &n
}

func getKey(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setKey(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
}

func deleteKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
