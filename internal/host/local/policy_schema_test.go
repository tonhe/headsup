package local

import (
	"bytes"
	"testing"
)

// PolicySchema must not modify its input: Plan passes the bytes that Apply
// later writes, and an in-place Standardize would strip the comments.
func TestPolicySchemaLeavesInputIntact(t *testing.T) {
	in := []byte("{\n  // allow all\n  \"acls\": [],\n}\n")
	want := bytes.Clone(in)
	if issues := PolicySchema(in); len(issues) != 0 {
		t.Fatalf("issues: %v", issues)
	}
	if !bytes.Equal(in, want) {
		t.Errorf("input changed:\n%q\nwant\n%q", in, want)
	}
}
