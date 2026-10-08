package catalog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUnknownPackage reports lookups of packages the catalog lacks.
type ErrUnknownPackage struct {
	Name string
}

func (e ErrUnknownPackage) Error() string {
	return fmt.Sprintf("catalog: unknown package %q", e.Name)
}

// Catalog is the loaded curated set: manifests indexed by package
// name. Local files only (ADR-014) — no remotes to trust, no caches
// to poison. Entries carry their versions inside (S150); the loader
// surfaces them verbatim rather than resolving ranges, because
// curated means pinned.
type Catalog struct {
	entries map[string]Manifest
}

// Load reads every manifest in dir: one `<name>.yaml` per package,
// strict fields (typos in curated files fail loudly), filename bound
// to manifest name (a file never smuggles another package), each
// entry validated. Non-YAML and dotfiles stay out without comment —
// editors and READMEs share the directory freely.
func Load(dir string) (Catalog, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Catalog{}, fmt.Errorf("catalog: read directory: %w", err)
	}
	catalog := Catalog{entries: map[string]Manifest{}}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") ||
			strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".yaml")
		manifest, err := loadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return Catalog{}, fmt.Errorf("catalog: %s: %w", entry.Name(), err)
		}
		if manifest.Name != name {
			return Catalog{}, fmt.Errorf("catalog: %s names %q, want %q", entry.Name(), manifest.Name, name)
		}
		if _, exists := catalog.entries[name]; exists {
			return Catalog{}, fmt.Errorf("catalog: duplicate package %q", name)
		}
		catalog.entries[name] = manifest
	}
	return catalog, nil
}

// loadFile parses and validates one manifest with unknown fields
// rejected: curated files have exactly the schema, nothing adjacent.
func loadFile(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read: %w", err)
	}
	var manifest Manifest
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Get returns one package or an unknown-package error.
func (c Catalog) Get(name string) (Manifest, error) {
	manifest, ok := c.entries[name]
	if !ok {
		return Manifest{}, ErrUnknownPackage{Name: name}
	}
	return manifest, nil
}

// Names returns package names sorted.
func (c Catalog) Names() []string {
	names := make([]string, 0, len(c.entries))
	for name := range c.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Empty reports a catalog with no packages: valid (nothing curated
// yet), never an error.
func (c Catalog) Empty() bool {
	return len(c.entries) == 0
}
