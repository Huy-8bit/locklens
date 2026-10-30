package queries

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every .sql file is embedded by a Go file of this package: an orphan file
// would be edited without effect.
func TestEverySQLFileIsEmbedded(t *testing.T) {
	var embedded strings.Builder
	goFiles, _ := filepath.Glob("*.go")
	for _, f := range goFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		embedded.Write(b)
	}
	n := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return err
		}
		n++
		if !strings.Contains(embedded.String(), "//go:embed "+filepath.ToSlash(path)+"\n") {
			t.Errorf("%s is not embedded", path)
		}
		return nil
	})
	if err != nil || n < 30 {
		t.Fatalf("walk: %d files, %v", n, err)
	}
}
