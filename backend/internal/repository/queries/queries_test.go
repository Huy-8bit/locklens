package queries

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	if err != nil || n < 150 {
		t.Fatalf("walk: %d files, %v", n, err)
	}
}

var placeholder = regexp.MustCompile(`\{\{\w+\}\}`)

func TestBuildersFillEveryPlaceholder(t *testing.T) {
	built := []string{ListQueries("mean_ratio", true), ListQueries("unknown", false), RawSeries("time_weighted_mean"),
		RollupSeries(HourRollupTable, "max"), RollupSeries("anything else", "last"), DeleteOlderThan("metric_points"),
		DeleteTargetRows("findings"), EnableCompression("metric_points", "target_id, metric")}
	for d := range LoadDimensions {
		q, err := LoadRows(d)
		if err != nil {
			t.Fatal(err)
		}
		built = append(built, q)
	}
	for _, q := range built {
		if placeholder.MatchString(q) {
			t.Errorf("unfilled placeholder in:\n%s", q)
		}
	}
	if _, err := LoadRows("nope"); err == nil {
		t.Fatal("unknown dimension accepted")
	}
	if !strings.Contains(ListQueries("unknown", false), "ORDER BY cur.total_ms ASC") {
		t.Fatal("unknown sort must fall back to total time")
	}
	if !strings.Contains(RollupSeries("x; DROP TABLE y", "sum"), "FROM metric_rollups_1m") {
		t.Fatal("rollup tables come from the fixed list only")
	}
}
