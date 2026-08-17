// Package ini implements a strict INI parser and a multi-document merge with
// conflict reporting.
//
// An INI document is a sequence of lines. Blank lines and full-line comments
// (lines whose first non-whitespace character is '#' or ';') are ignored. A
// section header has the form "[name]". Lines before any section header belong
// to the unnamed global section, whose name is the empty string.
//
// A key/value line is split on the first '='. The left side, trimmed of
// surrounding whitespace, is the key. The right side is the raw value, parsed
// by parseValue (quoted values honor escapes and preserve '#'/';' literally;
// unquoted values strip an inline comment that is a '#' or ';' at the value
// start or preceded by whitespace).
//
// Parse is strict: duplicate section headers, duplicate keys within a section,
// missing '=', empty keys, malformed section headers, and value errors
// (unterminated quote, invalid escape, trailing content after a quoted value)
// all return a *ParseError carrying the 1-based line number.
package ini

import (
	"fmt"
	"strings"
)

// KeyVal is one key/value pair within a section.
type KeyVal struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Section is a named group of ordered key/value pairs.
type Section struct {
	Name string   `json:"name"`
	Keys []KeyVal `json:"keys"`
}

// Document is an ordered list of sections.
type Document struct {
	Sections []Section `json:"sections"`
}

// ParseError describes a parse failure at a specific 1-based line.
type ParseError struct {
	Line int    `json:"line"`
	Msg  string `json:"error"`
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
}

// Parse parses INI text into a Document. The returned error, when non-nil, is
// always a *ParseError.
func Parse(content string) (*Document, error) {
	doc := &Document{Sections: []Section{}}
	sectionIndex := map[string]int{}        // section name -> index in doc.Sections
	keySeen := map[string]map[string]bool{} // section name -> set of keys seen
	allKeysSeen := map[string]bool{}

	current := "" // global section by default

	lines := strings.Split(content, "\n")
	for i, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if trimmed[0] == '#' || trimmed[0] == ';' {
			continue
		}

		if trimmed[0] == '[' {
			name, err := parseSectionHeader(trimmed)
			if err != nil {
				return nil, &ParseError{Line: i + 1, Msg: err.Error()}
			}
			if _, ok := sectionIndex[name]; ok {
				return nil, &ParseError{Line: i + 1, Msg: "duplicate section: " + name}
			}
			doc.Sections = append(doc.Sections, Section{Name: name, Keys: nil})
			sectionIndex[name] = len(doc.Sections) - 1
			keySeen[name] = map[string]bool{}
			current = name
			continue
		}

		// Key/value line: split on the first '='.
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, &ParseError{Line: i + 1, Msg: "missing '=' in line"}
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			return nil, &ParseError{Line: i + 1, Msg: "empty key"}
		}

		// Lazily materialize the current section (only needed for the global
		// section, which has no explicit header).
		idx, ok := sectionIndex[current]
		if !ok {
			doc.Sections = append(doc.Sections, Section{Name: current, Keys: []KeyVal{}})
			idx = len(doc.Sections) - 1
			sectionIndex[current] = idx
			keySeen[current] = map[string]bool{}
		}
		if allKeysSeen[key] {
			return nil, &ParseError{Line: i + 1, Msg: "duplicate key in section " + repr(current) + ": " + key}
		}
		value, err := parseValue(line[eq+1:])
		if err != nil {
			return nil, &ParseError{Line: i + 1, Msg: err.Error()}
		}
		keySeen[current][key] = true
		allKeysSeen[key] = true
		doc.Sections[idx].Keys = append(doc.Sections[idx].Keys, KeyVal{Key: key, Value: value})
	}
	return doc, nil
}

// parseSectionHeader parses a trimmed line that starts with '[' into a section
// name. It validates the closing bracket, the name, and any trailing content.
func parseSectionHeader(trimmed string) (string, error) {
	close := strings.IndexByte(trimmed, ']')
	if close < 0 {
		return "", fmt.Errorf("malformed section header: missing ']'")
	}
	name := strings.TrimSpace(trimmed[1:close])
	rest := strings.TrimLeft(trimmed[close+1:], " \t")
	if rest != "" && rest[0] != '#' && rest[0] != ';' {
		return "", fmt.Errorf("unexpected content after section header")
	}
	if name == "" {
		return "", fmt.Errorf("empty section name")
	}
	if strings.ContainsAny(name, "[]") {
		return "", fmt.Errorf("malformed section name: %s", name)
	}
	return name, nil
}

// parseValue parses the raw text after the first '=' on a line.
func parseValue(rawVal string) (string, error) {
	s := strings.TrimLeft(rawVal, " \t")
	if s == "" {
		return "", nil
	}
	if s[0] == '"' {
		return parseQuoted(s)
	}
	return parseUnquoted(s)
}

// parseQuoted parses a double-quoted value with \" \\ \n \t escapes.
func parseQuoted(s string) (string, error) {
	var b strings.Builder
	i := 1
	for i < len(s) {
		c := s[i]
		if c == '"' {
			rest := strings.TrimLeft(s[i+1:], " \t")
			if rest != "" && rest[0] != '#' && rest[0] != ';' {
				return "", fmt.Errorf("unexpected content after quoted value")
			}
			return b.String(), nil
		}
		if c == '\\' {
			if i+1 >= len(s) {
				return "", fmt.Errorf("dangling escape at end of value")
			}
			switch s[i+1] {
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", fmt.Errorf("invalid escape \\%c", s[i+1])
			}
			i += 2
			continue
		}
		b.WriteByte(c)
		i++
	}
	return "", fmt.Errorf("unterminated quoted value")
}

// parseUnquoted parses an unquoted value, stripping an inline comment that is a
// '#' or ';' at the value start or preceded by whitespace.
func parseUnquoted(s string) (string, error) {
	// A '#' or ';' at the very start makes the whole value a comment.
	if s[0] == '#' || s[0] == ';' {
		return "", nil
	}
	cut := len(s)
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c == '#' || c == ';') && (s[i-1] == ' ' || s[i-1] == '\t') {
			cut = i
			break
		}
	}
	return strings.TrimRight(s[:cut], " \t"), nil
}

// repr quotes a section name for inclusion in error messages so the global
// section "" reads as "".
func repr(name string) string {
	return fmt.Sprintf("%q", name)
}
