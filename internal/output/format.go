package output

import (
	"fmt"
	"os"
	"strings"
)

type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
)

func ParseFormat(value string) (Format, error) {
	if value == "" {
		return FormatTable, nil
	}
	switch strings.ToLower(value) {
	case string(FormatTable):
		return FormatTable, nil
	case string(FormatJSON):
		return FormatJSON, nil
	case string(FormatYAML):
		return FormatYAML, nil
	default:
		return "", fmt.Errorf("unsupported output format: %s", value)
	}
}

func Print(format Format, data any) error {
	switch format {
	case FormatTable:
		table, ok := data.(Table)
		if !ok {
			if tablePtr, ok := data.(*Table); ok && tablePtr != nil {
				table = *tablePtr
			} else {
				return fmt.Errorf("table output requires output.Table")
			}
		}
		return table.Render(os.Stdout)
	case FormatJSON:
		return PrintJSON(data)
	case FormatYAML:
		return PrintYAML(data)
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}
