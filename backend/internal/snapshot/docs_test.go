package snapshot

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/locklens/locklens/internal/model"
)

// TestDocumentedExampleMatchesSerializer keeps docs/snapshot-format.md honest:
// its example must decode into model.Snapshot with no unknown fields, must be
// identical to docs/examples/snapshot-chain.json, and must re-encode to the
// same bytes (so no documented field is missing from the serializer).
func TestDocumentedExampleMatchesSerializer(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/snapshot-format.md")
	if err != nil {
		t.Fatal(err)
	}
	const marker = "<!-- example:snapshot -->\n```json\n"
	i := bytes.Index(doc, []byte(marker))
	if i < 0 {
		t.Fatal("example marker not found in docs/snapshot-format.md")
	}
	rest := doc[i+len(marker):]
	example := rest[:bytes.Index(rest, []byte("\n```"))]

	stored, err := os.ReadFile("../../../docs/examples/snapshot-chain.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(example)) != strings.TrimSpace(string(stored)) {
		t.Fatal("documented example differs from docs/examples/snapshot-chain.json")
	}

	dec := json.NewDecoder(bytes.NewReader(example))
	dec.DisallowUnknownFields()
	var snap model.Snapshot
	if err := dec.Decode(&snap); err != nil {
		t.Fatalf("documented example does not match model.Snapshot: %v", err)
	}
	if snap.SchemaVersion != model.SchemaVersion {
		t.Errorf("example schema_version = %d", snap.SchemaVersion)
	}
	again, err := json.MarshalIndent(&snap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(again)) != strings.TrimSpace(string(example)) {
		t.Error("re-encoding the example changes it; docs and serializer disagree")
	}
	if _, err := Load("../../../docs/examples/snapshot-chain.json"); err != nil {
		t.Errorf("Load: %v", err)
	}
}
