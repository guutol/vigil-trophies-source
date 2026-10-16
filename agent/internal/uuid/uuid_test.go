package uuid

import (
	"regexp"
	"testing"
)

var v4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewIsV4AndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id, err := New()
		if err != nil {
			t.Fatal(err)
		}
		if !v4.MatchString(id) {
			t.Fatalf("not a v4 UUID: %s", id)
		}
		if seen[id] {
			t.Fatalf("duplicate: %s", id)
		}
		seen[id] = true
	}
}

func TestNewV5MatchesRFCVector(t *testing.T) {
	dns := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	// Same value as Python's uuid.uuid5(uuid.NAMESPACE_DNS, "python.org").
	if got := NewV5(dns, "python.org").String(); got != "886313e1-3b8a-5372-9b90-0c9aee199e5d" {
		t.Fatalf("got %s", got)
	}
	if NewV5(dns, "a") == NewV5(dns, "b") {
		t.Fatal("different names must give different ids")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "not-a-uuid", "886313e13b8a53729b900c9aee199e5d", "886313e1-3b8a-5372-9b90-0c9aee199e5z"} {
		if _, err := Parse(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
