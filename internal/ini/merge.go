package ini

import (
	"fmt"
)

// Strategy selects how a merge treats a (section, key) whose new value differs
// from the value already established by an earlier config.
type Strategy int

const (
	// LastWins overwrites with the later value and records a Conflict entry.
	LastWins Strategy = iota
	// FailOnConflict aborts the merge on the first differing conflict, returning
	// a *ConflictError.
	FailOnConflict
)

// ParseStrategy maps the JSON strategy string to a Strategy. The bool is false
// when the string is not a recognized strategy.
func ParseStrategy(s string) (Strategy, bool) {
	switch s {
	case "last-wins":
		return LastWins, true
	case "fail-on-conflict":
		return FailOnConflict, true
	default:
		return LastWins, false
	}
}

// String returns the JSON strategy string for a Strategy.
func (s Strategy) String() string {
	switch s {
	case LastWins:
		return "last-wins"
	case FailOnConflict:
		return "fail-on-conflict"
	default:
		return "last-wins"
	}
}

// NamedDoc pairs a parsed document with the name of the config it came from, so
// conflicts can report provenance.
type NamedDoc struct {
	Name     string
	Document *Document
}

// Conflict records one override of an existing (section, key) by a later
// config with a differing value.
type Conflict struct {
	Section      string `json:"section"`
	Key          string `json:"key"`
	From         string `json:"from"`
	OldValue     string `json:"old_value"`
	OverriddenBy string `json:"overridden_by"`
	NewValue     string `json:"new_value"`
}

// ConflictError is returned by Merge when strategy is FailOnConflict and at
// least one differing conflict is found. It carries the conflicts seen up to
// (and including) the first one.
type ConflictError struct {
	Conflicts []Conflict
}

func (e *ConflictError) Error() string {
	if len(e.Conflicts) == 0 {
		return "merge conflict"
	}
	c := e.Conflicts[len(e.Conflicts)-1]
	return fmt.Sprintf("merge conflict in section %q key %q: %q (%s) -> %q (%s)",
		c.Section, c.Key, c.OldValue, c.From, c.NewValue, c.OverriddenBy)
}

// MergeResult is the outcome of a successful (non-aborting) merge.
type MergeResult struct {
	Merged    *Document  `json:"merged"`
	Conflicts []Conflict `json:"conflicts"`
}

// Merge combines the named documents in order. Sections and keys appear in
// first-seen order. A later config that re-establishes a (section, key) with a
// different value is a conflict, handled per strategy. A later config that
// re-establishes the same value is a no-op (no conflict).
func Merge(docs []NamedDoc, strategy Strategy) (*MergeResult, error) {
	merged := &Document{Sections: []Section{}}
	sectionIndex := map[string]int{}          // section name -> index in merged.Sections
	values := map[string]map[string]string{}  // section -> key -> current value
	sources := map[string]map[string]string{} // section -> key -> current source name
	keyOrder := map[string][]string{}         // section -> ordered keys
	var conflicts []Conflict

	for _, nd := range docs {
		for _, sec := range nd.Document.Sections {
			if _, ok := sectionIndex[sec.Name]; !ok {
				merged.Sections = append(merged.Sections, Section{Name: sec.Name})
				sectionIndex[sec.Name] = len(merged.Sections) - 1
				values[sec.Name] = map[string]string{}
				sources[sec.Name] = map[string]string{}
			}
			for _, kv := range sec.Keys {
				old, exists := values[sec.Name][kv.Key]
				if !exists {
					values[sec.Name][kv.Key] = kv.Value
					sources[sec.Name][kv.Key] = nd.Name
					keyOrder[sec.Name] = append(keyOrder[sec.Name], kv.Key)
					continue
				}
				if old == kv.Value {
					// Same value from a later config: not a conflict.
					continue
				}
				conflict := Conflict{
					Section:      sec.Name,
					Key:          kv.Key,
					From:         sources[sec.Name][kv.Key],
					OldValue:     old,
					OverriddenBy: nd.Name,
					NewValue:     kv.Value,
				}
				if strategy == FailOnConflict {
					return nil, &ConflictError{Conflicts: []Conflict{conflict}}
				}
				values[sec.Name][kv.Key] = kv.Value
				sources[sec.Name][kv.Key] = nd.Name
				conflicts = []Conflict{conflict}
			}
		}
	}

	// Flatten the per-section ordered keys into the merged sections.
	for i, sec := range merged.Sections {
		keys := keyOrder[sec.Name]
		out := make([]KeyVal, 0, len(keys))
		for _, k := range keys {
			out = append(out, KeyVal{Key: k, Value: values[sec.Name][k]})
		}
		merged.Sections[i].Keys = out
	}

	if conflicts == nil {
		conflicts = []Conflict{}
	}
	return &MergeResult{Merged: merged, Conflicts: conflicts}, nil
}
