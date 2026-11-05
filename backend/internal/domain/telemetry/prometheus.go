package telemetry

import (
	"bufio"
	"io"
	"math"
	"strconv"
	"strings"
)

// ContentType is the Prometheus text exposition format.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// WritePrometheus writes families in the Prometheus text format.
func WritePrometheus(w io.Writer, fams []Family) error {
	b := bufio.NewWriter(w)
	for _, f := range fams {
		b.WriteString("# HELP " + f.Name + " " + escapeHelp(f.Help) + "\n")
		b.WriteString("# TYPE " + f.Name + " " + f.Type + "\n")
		for _, s := range f.Samples {
			b.WriteString(f.Name)
			if len(s.Labels) > 0 {
				b.WriteByte('{')
				for i, l := range s.Labels {
					if i > 0 {
						b.WriteByte(',')
					}
					b.WriteString(l.Name + `="` + escapeLabel(l.Value) + `"`)
				}
				b.WriteByte('}')
			}
			b.WriteString(" " + formatValue(s.Value) + "\n")
		}
	}
	return b.Flush()
}

func escapeHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

func escapeLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(s)
}

func formatValue(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
