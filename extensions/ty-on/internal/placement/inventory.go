package placement

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Inventory mirrors the parts of `on`'s hosts.yaml this resolver cares about.
// Unknown keys are ignored, so `on` can grow its schema without breaking us.
//
// See github.com/bborn/on for the full documented shape.
type Inventory struct {
	// Repos maps a project name to its clone URL. Used by `on` when a host
	// does not have the project yet; the resolver only reads Hosts.
	Repos map[string]string `yaml:"repos"`
	Hosts map[string]Host   `yaml:"hosts"`
}

// Host is one machine in the fleet.
type Host struct {
	SSH          string   `yaml:"ssh"`
	Workdir      string   `yaml:"workdir"`
	Capabilities []string `yaml:"capabilities"`
	// Repos maps a project name to that project's checkout path on this host.
	Repos map[string]string `yaml:"repos"`
}

// Candidate is a host that has a checkout of the project being placed.
type Candidate struct {
	// Name is the inventory key, which is also the name `on` accepts.
	Name string
	Host Host
	// Checkout is the project's path on the host, from the host's repos map.
	Checkout string
}

// InventoryPath resolves the inventory location the same way `on` does:
// ON_HOSTS wins, then $XDG_CONFIG_HOME/on/hosts.yaml, then ~/.config/on/hosts.yaml.
func InventoryPath() string {
	if p := os.Getenv("ON_HOSTS"); p != "" {
		return p
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "on", "hosts.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Nothing sensible to fall back to; the caller will report the miss.
		return filepath.Join(".config", "on", "hosts.yaml")
	}
	return filepath.Join(home, ".config", "on", "hosts.yaml")
}

// LoadInventory reads and parses the inventory at path. Its errors are written
// to be shown to a user verbatim as a placement reason.
func LoadInventory(path string) (*Inventory, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-controlled config, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no host inventory at %s, nothing to place onto", path)
		}
		return nil, fmt.Errorf("host inventory at %s could not be read: %v", path, err)
	}

	var inv Inventory
	if err := yaml.Unmarshal(data, &inv); err != nil {
		return nil, fmt.Errorf("host inventory at %s is not valid YAML: %v", path, err)
	}
	return &inv, nil
}

// Serving returns the hosts that have a checkout of project, sorted by name so
// the answer does not depend on Go's map iteration order.
func (inv *Inventory) Serving(project string) []Candidate {
	var out []Candidate
	for name, host := range inv.Hosts {
		checkout, ok := host.Repos[project]
		if !ok || checkout == "" {
			continue
		}
		out = append(out, Candidate{Name: name, Host: host, Checkout: checkout})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
