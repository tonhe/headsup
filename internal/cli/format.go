package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v3"
)

// Output formats for -o.
const (
	fmtTable = "table"
	fmtWide  = "wide"
	fmtJSON  = "json"
	fmtYAML  = "yaml"
	fmtRaw   = "raw"
)

// table is a header and rows of cells.
type table struct {
	head []string
	rows [][]string
}

func (t *table) add(cells ...string) { t.rows = append(t.rows, cells) }

// write prints the table aligned on two-space gutters. Empty cells print
// as "-" so columns stay parseable with awk.
func (t table) write(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	line := func(cells []string) {
		out := make([]string, len(cells))
		for i, c := range cells {
			c = strings.ReplaceAll(c, "\t", " ")
			if c == "" {
				c = "-"
			}
			out[i] = c
		}
		fmt.Fprintln(tw, strings.Join(out, "\t"))
	}
	if len(t.head) > 0 {
		line(t.head)
	}
	for _, r := range t.rows {
		line(r)
	}
	return tw.Flush()
}

// kvTable is a two-column key/value listing for single objects.
func kvTable(pairs ...[2]string) table {
	t := table{}
	for _, p := range pairs {
		t.add(p[0], p[1])
	}
	return t
}

// emit prints data in the chosen format: json and yaml encode data (stable
// snake_case field names), table and wide call tbl.
func emit(w io.Writer, format string, data any, tbl func(wide bool) table) error {
	switch format {
	case fmtJSON:
		b, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\n", b)
		return err
	case fmtYAML:
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(data); err != nil {
			return err
		}
		_ = enc.Close()
		_, err := w.Write(buf.Bytes())
		return err
	case fmtWide:
		return tbl(true).write(w)
	case fmtTable, "":
		return tbl(false).write(w)
	}
	return usagef("unknown output format %q (table, wide, json, yaml)", format)
}

func checkFormat(f string, allowed ...string) error {
	for _, a := range allowed {
		if f == a {
			return nil
		}
	}
	return usagef("-o %s is not supported here (use %s)", f, strings.Join(allowed, ", "))
}

// ts formats a timestamp for json/yaml: RFC 3339 in UTC, nil when unset.
func ts(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// cellTime formats a timestamp for a table cell.
func cellTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// nonNil keeps empty lists as [] rather than null in json.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
