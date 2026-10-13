package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExpandHome resolves a leading "~" against the user's home directory.
//
// The obvious thing to write in a config file is `~/Music`, so the setting has
// to accept it. Without this the path is handed to the scanner verbatim, the
// scan reports the root unreachable, and the application blames a directory
// that does exist.
//
// Only "~" and "~/..." are expanded. "~otheruser/..." is left alone rather than
// guessed at: resolving another account's home is a look-up with real failure
// modes, and a path we decline to expand fails loudly rather than quietly
// pointing at the wrong directory.
func ExpandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	if len(path) > 1 && path[1] != '/' && !os.IsPathSeparator(path[1]) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if len(path) == 1 {
		return home
	}
	return filepath.Join(home, path[2:])
}

// SaveLibraryRoot writes ui.library_root into the config file at path,
// creating the file and its directory if they do not exist.
//
// It round-trips through yaml.Node rather than re-marshalling the decoded
// Config. Marshalling the struct would be shorter, but decoding fills every
// unspecified field from Defaults, so a save from inside the application would
// rewrite the user's file as a full defaults dump: their comments gone, their
// field order gone, and every default they never asked about now stated
// explicitly -- which, given that unknown fields are errors, means the next
// hand edit is made against a file this program invented. Editing the node
// tree in place keeps everything the user wrote.
func SaveLibraryRoot(path, root string) error {
	if path == "" {
		return errors.New("no config file path: pass -config, or set XDG_CONFIG_HOME")
	}
	if root == "" {
		return errors.New("library root cannot be empty")
	}

	doc, err := readDocument(path)
	if err != nil {
		return err
	}
	m, err := documentMapping(path, doc)
	if err != nil {
		return err
	}
	ui, err := ensureMap(m, "ui")
	if err != nil {
		return fmt.Errorf("%s: ui: %w", path, err)
	}
	setScalar(ui, "library_root", root)

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// readDocument parses the config file into a YAML node tree.
//
// A missing file yields an empty document rather than an error: the settings
// screen has to be able to create a config on a fresh install, and that is the
// only state in which a user most wants to set a library root.
func readDocument(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &yaml.Node{Kind: yaml.DocumentNode}, nil
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if doc.Kind == 0 {
		// An empty file decodes to the zero node. Treat it as an empty
		// document: "all defaults" is a valid config, and so is a config with
		// exactly one setting in it.
		doc = yaml.Node{Kind: yaml.DocumentNode}
	}
	return &doc, nil
}

// documentMapping returns the top-level mapping of a document.
func documentMapping(path string, doc *yaml.Node) (*yaml.Node, error) {
	if len(doc.Content) == 0 {
		// Nothing to edit into: append an empty mapping.
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Content = append(doc.Content, m)
		return m, nil
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: expected a YAML mapping at the top level", path)
	}
	return m, nil
}

// ensureMap returns the mapping stored at key, creating it when absent.
//
// A non-mapping value under the key is an error rather than something to
// replace: `ui: 3` in a config file means the user wrote something else, and
// overwriting it with a mapping would discard their input to make a save
// succeed.
func ensureMap(m *yaml.Node, key string) (*yaml.Node, error) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		v := m.Content[i+1]
		if v.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%q is not a mapping", key)
		}
		return v, nil
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		child,
	)
	return child, nil
}

// setScalar sets key in a mapping node to a string, or adds it.
//
// An existing value is edited in place rather than replaced, so the comments
// attached to it survive: a user who annotated their library root would
// otherwise lose the annotation to a save from inside the application.
func setScalar(m *yaml.Node, key, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != key {
			continue
		}
		v := m.Content[i+1]
		v.Kind = yaml.ScalarNode
		v.Tag = "!!str"
		v.Style = 0
		v.Value = value
		v.Content = nil
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

// ResolveLibraryRoot picks the library root to scan.
//
// The -library flag wins: a path typed at the prompt is an explicit request for
// this run, and it should not be overridden by a config file the user may not
// remember setting. The config value is the fallback, which is the whole point
// of the setting. "~" is expanded in both cases.
func ResolveLibraryRoot(flagRoot, configRoot string) string {
	root := strings.TrimSpace(flagRoot)
	if root == "" {
		root = strings.TrimSpace(configRoot)
	}
	return ExpandHome(root)
}
