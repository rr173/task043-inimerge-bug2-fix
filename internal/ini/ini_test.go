package ini

import (
	"strings"
	"testing"
)

func TestParseSectionsAndKeys(t *testing.T) {
	doc, err := Parse("[db]\nhost = localhost\nport = 5432\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(doc.Sections) != 1 {
		t.Fatalf("sections=%d want 1", len(doc.Sections))
	}
	if doc.Sections[0].Name != "db" {
		t.Fatalf("name=%q want db", doc.Sections[0].Name)
	}
	if len(doc.Sections[0].Keys) != 2 {
		t.Fatalf("keys=%d want 2", len(doc.Sections[0].Keys))
	}
	if doc.Sections[0].Keys[0].Key != "host" || doc.Sections[0].Keys[0].Value != "localhost" {
		t.Errorf("key0=%+v", doc.Sections[0].Keys[0])
	}
	if doc.Sections[0].Keys[1].Key != "port" || doc.Sections[0].Keys[1].Value != "5432" {
		t.Errorf("key1=%+v", doc.Sections[0].Keys[1])
	}
}

func TestParseGlobalSection(t *testing.T) {
	doc, err := Parse("name = app\n[db]\nhost = x\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(doc.Sections) != 2 {
		t.Fatalf("sections=%d want 2", len(doc.Sections))
	}
	if doc.Sections[0].Name != "" {
		t.Errorf("global name=%q want empty", doc.Sections[0].Name)
	}
	if len(doc.Sections[0].Keys) != 1 || doc.Sections[0].Keys[0].Key != "name" {
		t.Errorf("global keys=%+v", doc.Sections[0].Keys)
	}
}

func TestParseQuotedValuePreservesHash(t *testing.T) {
	doc, err := Parse(`path = "/x # not a comment"` + "\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := doc.Sections[0].Keys[0].Value; got != "/x # not a comment" {
		t.Errorf("value=%q", got)
	}
}

func TestParseInlineCommentStripped(t *testing.T) {
	cases := map[string]string{
		"path = /x # comment\n":  "/x",
		"path = /x ; comment\n":  "/x",
		"path = /x#frag\n":       "/x#frag",  // '#' not preceded by whitespace
		"path = http://x#frag\n": "http://x#frag",
		"path = # whole comment\n": "", // '#' at value start
		"path =\n":               "",
	}
	for in, want := range cases {
		doc, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if got := doc.Sections[0].Keys[0].Value; got != want {
			t.Errorf("Parse(%q): value=%q want %q", in, got, want)
		}
	}
}

func TestParseEscapes(t *testing.T) {
	// "a\"b\\c\nd"  ->  a"b\c<LF>d
	doc, err := Parse("key = \"a\\\"b\\\\c\\nd\"\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := doc.Sections[0].Keys[0].Value; got != "a\"b\\c\nd" {
		t.Errorf("value=%q", got)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		line    int
		needle  string
	}{
		{"duplicate key", "[s]\nk = 1\nk = 2\n", 3, "duplicate key"},
		{"duplicate section", "[a]\nx = 1\n[a]\ny = 2\n", 3, "duplicate section"},
		{"unterminated quote", "key = \"oops\n", 1, "unterminated"},
		{"invalid escape", "key = \"a\\xb\"\n", 1, "escape"},
		{"missing equals", "notakeyvalue\n", 1, "missing '='"},
		{"empty key", " = value\n", 1, "empty key"},
		{"empty section name", "[]\nx = 1\n", 1, "empty section name"},
		{"trailing after quote", "key = \"a\" b\n", 1, "unexpected content"},
		{"malformed section header", "[unterminated\nx = 1\n", 1, "missing ']'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.content)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			pe, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("error is not *ParseError: %T", err)
			}
			if pe.Line != tc.line {
				t.Errorf("line=%d want %d", pe.Line, tc.line)
			}
			if !strings.Contains(pe.Msg, tc.needle) {
				t.Errorf("msg=%q want substring %q", pe.Msg, tc.needle)
			}
		})
	}
}

func TestMergeLastWins(t *testing.T) {
	docs := []NamedDoc{
		{Name: "base", Document: mustParse(t, "[s]\nk = 1\nm = 2\n")},
		{Name: "over", Document: mustParse(t, "[s]\nk = 9\n")},
	}
	res, err := Merge(docs, LastWins)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("conflicts=%d want 1", len(res.Conflicts))
	}
	c := res.Conflicts[0]
	if c.Section != "s" || c.Key != "k" || c.From != "base" || c.OldValue != "1" ||
		c.OverriddenBy != "over" || c.NewValue != "9" {
		t.Errorf("conflict=%+v", c)
	}
	sec := res.Merged.Sections[0]
	if len(sec.Keys) != 2 {
		t.Fatalf("keys=%d want 2", len(sec.Keys))
	}
	if sec.Keys[0].Key != "k" || sec.Keys[0].Value != "9" {
		t.Errorf("k=%+v want 9", sec.Keys[0])
	}
	if sec.Keys[1].Key != "m" || sec.Keys[1].Value != "2" {
		t.Errorf("m=%+v want 2", sec.Keys[1])
	}
}

func TestMergeFailOnConflict(t *testing.T) {
	docs := []NamedDoc{
		{Name: "a", Document: mustParse(t, "[s]\nk = 1\n")},
		{Name: "b", Document: mustParse(t, "[s]\nk = 2\n")},
	}
	_, err := Merge(docs, FailOnConflict)
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("error is not *ConflictError: %T", err)
	}
	if len(ce.Conflicts) != 1 {
		t.Fatalf("conflicts=%d want 1", len(ce.Conflicts))
	}
}

func TestMergeSameValueNoConflict(t *testing.T) {
	docs := []NamedDoc{
		{Name: "a", Document: mustParse(t, "[s]\nk = 1\n")},
		{Name: "b", Document: mustParse(t, "[s]\nk = 1\n")},
	}
	for _, strat := range []Strategy{LastWins, FailOnConflict} {
		res, err := Merge(docs, strat)
		if err != nil {
			t.Fatalf("strategy %v: %v", strat, err)
		}
		if len(res.Conflicts) != 0 {
			t.Errorf("strategy %v: conflicts=%v want empty", strat, res.Conflicts)
		}
	}
}

func mustParse(t *testing.T, content string) *Document {
	t.Helper()
	doc, err := Parse(content)
	if err != nil {
		t.Fatalf("mustParse: %v", err)
	}
	return doc
}
