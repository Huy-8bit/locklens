package changes

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
)

// Schema is the part of a database's catalog that schema change detection
// compares: table names and index definitions with their validity.
type Schema struct {
	At       time.Time `json:"at"`
	DatName  string    `json:"datname"`
	Complete bool      `json:"complete"`
	Tables   []string  `json:"tables"`
	// Indexes maps schema.index to its definition; an invalid index's
	// definition ends with " (invalid)".
	Indexes map[string]string `json:"indexes"`
}

// SchemaOf extracts the comparable schema from a catalog.
func SchemaOf(c observe.Catalog) Schema {
	s := Schema{At: c.At, DatName: c.DatName, Complete: !c.Truncated, Indexes: map[string]string{}}
	for _, t := range c.Tables {
		if !t.PartitionedChild {
			s.Tables = append(s.Tables, t.Qualified())
		}
	}
	slices.Sort(s.Tables)
	for _, ix := range c.Indexes {
		d := ix.Definition
		if !ix.Valid {
			d += invalidMark
		}
		s.Indexes[ix.Qualified()] = d
	}
	return s
}

const invalidMark = " (invalid)"

// ObjectChange is one created, dropped, or changed table or index.
type ObjectChange struct {
	Object     string `json:"object"` // table, index
	Name       string `json:"name"`
	Change     string `json:"change"` // created, dropped, invalid, valid, redefined
	Definition string `json:"definition,omitempty"`
}

// DiffSchema compares two readings of a database's schema. Both must be
// complete (not cut at the catalog limits): otherwise objects that only fell
// off the list would look dropped.
func DiffSchema(prev, cur Schema) []Event {
	if !prev.Complete || !cur.Complete || prev.DatName != cur.DatName {
		return nil
	}
	var ch []ObjectChange
	for _, t := range cur.Tables {
		if _, found := slices.BinarySearch(prev.Tables, t); !found {
			ch = append(ch, ObjectChange{Object: "table", Name: t, Change: "created"})
		}
	}
	for _, t := range prev.Tables {
		if _, found := slices.BinarySearch(cur.Tables, t); !found {
			ch = append(ch, ObjectChange{Object: "table", Name: t, Change: "dropped"})
		}
	}
	for name, d := range cur.Indexes {
		p, ok := prev.Indexes[name]
		pi, ci := strings.HasSuffix(p, invalidMark), strings.HasSuffix(d, invalidMark)
		switch {
		case !ok:
			ch = append(ch, ObjectChange{Object: "index", Name: name, Change: "created", Definition: d})
		case p == d:
		case !pi && ci:
			ch = append(ch, ObjectChange{Object: "index", Name: name, Change: "invalid", Definition: d})
		case pi && !ci:
			ch = append(ch, ObjectChange{Object: "index", Name: name, Change: "valid", Definition: d})
		default:
			ch = append(ch, ObjectChange{Object: "index", Name: name, Change: "redefined", Definition: d})
		}
	}
	for name, d := range prev.Indexes {
		if _, ok := cur.Indexes[name]; !ok {
			ch = append(ch, ObjectChange{Object: "index", Name: name, Change: "dropped", Definition: d})
		}
	}
	if len(ch) == 0 {
		return nil
	}
	slices.SortFunc(ch, func(a, b ObjectChange) int {
		return strings.Compare(a.Object+" "+a.Name, b.Object+" "+b.Name)
	})
	title := schemaTitle(cur.DatName, ch)
	if len(ch) > maxListed {
		ch = ch[:maxListed]
	}
	d := window(prev.At, cur.At)
	d["database"], d["changes"] = cur.DatName, ch
	return []Event{{At: cur.At, Kind: KindSchema, Source: SourceDetected, Title: title, Details: d}}
}

func schemaTitle(db string, ch []ObjectChange) string {
	if len(ch) == 1 {
		phrase := map[string]string{"invalid": "is invalid", "valid": "became valid", "redefined": "was redefined"}[ch[0].Change]
		if phrase == "" {
			phrase = ch[0].Change
		}
		return fmt.Sprintf("%s: %s %s %s", db, ch[0].Object, ch[0].Name, phrase)
	}
	counts := map[string]int{}
	var order []string
	for _, c := range ch {
		k := c.Object + " " + c.Change
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		obj, change, _ := strings.Cut(k, " ")
		if counts[k] > 1 {
			obj = map[string]string{"table": "tables", "index": "indexes"}[obj]
		}
		parts = append(parts, fmt.Sprintf("%d %s %s", counts[k], obj, change))
	}
	return fmt.Sprintf("%s: schema changed (%s)", db, strings.Join(parts, ", "))
}
