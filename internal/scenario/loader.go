package scenario

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxYAMLBytes = 1 << 20 // 1 MB

func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	lr := &io.LimitedReader{R: f, N: limit + 1}
	buf, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > limit {
		return nil, fmt.Errorf("%s: yaml exceeds %d bytes", path, limit)
	}
	return buf, nil
}

// File is a top-level YAML file: one or more scenarios in sequence plus an
// optional `shared` block whose fields default into every scenario below.
type File struct {
	Shared    sharedDefaults `yaml:"shared,omitempty"`
	Scenarios []Scenario     `yaml:"scenarios"`
}

type sharedDefaults struct {
	Tier                 Tier      `yaml:"tier,omitempty"`
	Role                 Role      `yaml:"role,omitempty"`
	AvailableTools       []ToolDef `yaml:"available_tools,omitempty"`
	ContextGrowthProfile string    `yaml:"context_growth_profile,omitempty"`
}

// LoadFile reads a YAML scenario file and returns the parsed scenarios with
// shared defaults folded in and Validate() called on each.
func LoadFile(path string) ([]Scenario, error) {
	raw, err := readCapped(path, maxYAMLBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f File
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("yaml %s: %w", path, err)
	}
	out := make([]Scenario, 0, len(f.Scenarios))
	for i := range f.Scenarios {
		s := f.Scenarios[i]
		if s.Tier == "" {
			s.Tier = f.Shared.Tier
		}
		if s.Role == "" {
			s.Role = f.Shared.Role
		}
		if len(s.AvailableTools) == 0 && len(f.Shared.AvailableTools) > 0 {
			s.AvailableTools = f.Shared.AvailableTools
		}
		if s.ContextGrowthProfile == "" && f.Shared.ContextGrowthProfile != "" {
			s.ContextGrowthProfile = f.Shared.ContextGrowthProfile
		}
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// LoadTree loads every *.yaml file under root recursively, skipping files
// whose basename is "system-prompt.md" or similar non-scenario assets.
// Results are sorted by (tier, id) for deterministic output ordering.
func LoadTree(root string) ([]Scenario, error) {
	var all []Scenario
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, "_") {
			// skip convention for fragment files
			return nil
		}
		loaded, loadErr := LoadFile(path)
		if loadErr != nil {
			return loadErr
		}
		all = append(all, loaded...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Tier != all[j].Tier {
			return tierOrder(all[i].Tier) < tierOrder(all[j].Tier)
		}
		return all[i].ID < all[j].ID
	})
	return all, nil
}

func tierOrder(t Tier) int {
	for i, x := range AllTiers() {
		if x == t {
			return i
		}
	}
	return 99
}

// LoadSharedTools loads a YAML file whose `tools:` key lists the 5 resolver
// tool definitions per spec §4.
func LoadSharedTools(path string) ([]ToolDef, error) {
	raw, err := readCapped(path, maxYAMLBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f struct {
		Tools []ToolDef `yaml:"tools"`
	}
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("yaml %s: %w", path, err)
	}
	return f.Tools, nil
}

// LoadSystemPrompt reads a system-prompt file verbatim. Callers that need
// byte-exactness against a pinned SHA can hash the return value.
func LoadSystemPrompt(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(raw), nil
}
