package corpus

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Configure merges an explicit approved-source inventory into the existing
// engine configuration. Call through governed execution, as for corpus index.
func Configure(homePath, input string) error {
	if input == "" {
		return fmt.Errorf("--file with approved corpus_lanes is required")
	}
	raw, err := os.ReadFile(input)
	if err != nil {
		return fmt.Errorf("cannot read source configuration")
	}
	var added struct {
		Lanes []LaneConfig `yaml:"corpus_lanes"`
	}
	if err := yaml.Unmarshal(raw, &added); err != nil {
		return fmt.Errorf("invalid source configuration")
	}
	if len(added.Lanes) == 0 {
		return fmt.Errorf("no approved sources")
	}
	for i, l := range added.Lanes {
		if l.Source == nil {
			return fmt.Errorf("each approved source needs root and format")
		}
		l, err = ValidateLaneConfig(l)
		if err != nil {
			return err
		}
		if l.Privacy != "private" || !laneIsPrivateLocal(l) {
			return fmt.Errorf("automatic capture requires private local embedding")
		}
		added.Lanes[i] = l
	}
	path := filepath.Join(homePath, "config", "memory-engine.yaml")
	doc := map[string]any{"schema_version": "compound.memory_engine_config.v0.1", "profile": "breadth"}
	existing := []LaneConfig{}
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() {
			return fmt.Errorf("configuration must be a regular file")
		}
		raw, err = os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = yaml.Unmarshal(raw, &doc); err != nil {
			return err
		}
		var old struct {
			Lanes []LaneConfig `yaml:"corpus_lanes"`
		}
		if err = yaml.Unmarshal(raw, &old); err != nil {
			return err
		}
		existing = old.Lanes
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, l := range added.Lanes {
		replaced := false
		for i, old := range existing {
			if old.Lane == l.Lane {
				if old.SourceFamily != l.SourceFamily {
					return fmt.Errorf("lane source family cannot change")
				}
				existing[i] = l
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, l)
		}
	}
	names, families := map[string]bool{}, map[string]bool{}
	for _, l := range existing {
		if names[l.Lane] {
			return fmt.Errorf("duplicate lane")
		}
		names[l.Lane] = true
		if l.Source != nil {
			if families[l.SourceFamily] {
				return fmt.Errorf("automatic sources must have distinct families")
			}
			families[l.SourceFamily] = true
		}
	}
	doc["corpus_lanes"] = existing
	raw, err = yaml.Marshal(doc)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".memory-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
