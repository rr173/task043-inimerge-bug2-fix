package ini

import "testing"

func TestProbeMergeKeepsAllConflictRecords(t *testing.T) {
	docs := []NamedDoc{
		{Name: "base", Document: mustProbeParse(t, "[s]\na = 1\nb = 1\n")},
		{Name: "override", Document: mustProbeParse(t, "[s]\na = 2\nb = 2\n")},
	}
	result, err := Merge(docs, LastWins)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(result.Conflicts) != 2 {
		t.Fatalf("conflicts=%d want 2: %+v", len(result.Conflicts), result.Conflicts)
	}
}

func mustProbeParse(t *testing.T, content string) *Document {
	t.Helper()
	doc, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}
