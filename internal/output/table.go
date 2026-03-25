package output

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

type Table struct {
	Headers []string
	Rows    [][]string
}

func (t Table) Render(w io.Writer) error {
	if len(t.Headers) == 0 {
		return fmt.Errorf("table headers required")
	}

	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, strings.Join(t.Headers, "\t")); err != nil {
		return fmt.Errorf("write headers: %w", err)
	}

	for _, row := range t.Rows {
		if len(row) != len(t.Headers) {
			return fmt.Errorf("row has %d columns, expected %d", len(row), len(t.Headers))
		}
		if _, err := fmt.Fprintln(writer, strings.Join(row, "\t")); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush table: %w", err)
	}

	return nil
}
