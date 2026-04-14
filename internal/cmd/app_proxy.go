package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/agynio/agyn-cli/internal/auth"
	"github.com/agynio/agyn-cli/internal/output"
	"github.com/spf13/cobra"
)

func newAppProxyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "app-proxy <slug> <command> [flags]",
		Aliases:            []string{"app"},
		Short:              "Invoke an app endpoint through the Gateway proxy",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if hasHelpArg() {
				return cmd.Help()
			}
			slug, command, payload, err := parseAppProxyArgs(args)
			if err != nil {
				return err
			}

			method, err := toPascalCase(command)
			if err != nil {
				return err
			}

			runContext, err := RunContextFrom(cmd)
			if err != nil {
				return err
			}
			baseURL := ""
			if runContext.Clients != nil {
				baseURL = runContext.Clients.BaseURL
			} else if runContext.Config != nil {
				baseURL = runContext.Config.ResolveGatewayURL("")
			}
			if baseURL == "" {
				return fmt.Errorf("gateway URL unavailable")
			}

			token, err := auth.LoadToken(auth.TokenOptions{})
			if err != nil {
				return err
			}

			body, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("marshal payload: %w", err)
			}

			url := fmt.Sprintf("%s/apps/%s/%s", strings.TrimRight(baseURL, "/"), slug, method)
			request, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, url, bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("build request: %w", err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+token)

			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return fmt.Errorf("send request: %w", err)
			}
			responseBody, err := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if err != nil {
				return fmt.Errorf("read response: %w", err)
			}
			if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
				trimmed := strings.TrimSpace(string(responseBody))
				if trimmed == "" {
					return fmt.Errorf("request failed: %s", response.Status)
				}
				return fmt.Errorf("request failed: %s: %s", response.Status, trimmed)
			}
			if closeErr != nil {
				return fmt.Errorf("close response body: %w", closeErr)
			}

			return printProxyResponse(runContext.OutputFormat, responseBody)
		},
	}

	return cmd
}

func parseAppProxyArgs(args []string) (string, string, map[string]any, error) {
	var slug string
	var command string
	payload := make(map[string]any)

	for i := 0; i < len(args); {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--"):
			key := strings.TrimPrefix(arg, "--")
			if key == "" {
				return "", "", nil, fmt.Errorf("empty flag name")
			}
			if i+1 >= len(args) {
				return "", "", nil, fmt.Errorf("missing value for flag %s", arg)
			}
			value := args[i+1]
			snakeKey, err := toSnakeCase(key)
			if err != nil {
				return "", "", nil, err
			}
			payload[snakeKey] = inferValue(value)
			i += 2
		case strings.HasPrefix(arg, "-"):
			return "", "", nil, fmt.Errorf("unsupported flag format: %s", arg)
		case slug == "":
			slug = arg
			i++
		case command == "":
			command = arg
			i++
		default:
			return "", "", nil, fmt.Errorf("unexpected argument: %s", arg)
		}
	}

	if slug == "" || command == "" {
		return "", "", nil, fmt.Errorf("slug and command are required")
	}

	return slug, command, payload, nil
}

func toPascalCase(input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("command is required")
	}
	parts := strings.Split(input, "-")
	var builder strings.Builder
	for _, part := range parts {
		if part == "" {
			return "", fmt.Errorf("invalid command: %s", input)
		}
		builder.WriteString(strings.ToUpper(part[:1]))
		if len(part) > 1 {
			builder.WriteString(part[1:])
		}
	}
	return builder.String(), nil
}

func toSnakeCase(input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("flag name is required")
	}
	return strings.ReplaceAll(input, "-", "_"), nil
}

func inferValue(value string) any {
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		return parsed
	}
	if strings.EqualFold(value, "true") {
		return true
	}
	if strings.EqualFold(value, "false") {
		return false
	}
	return value
}

func printProxyResponse(format output.Format, payload []byte) error {
	if format != output.FormatTable {
		_, err := os.Stdout.Write(appendTrailingNewline(payload))
		return err
	}

	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		_, err := os.Stdout.Write(appendTrailingNewline(payload))
		return err
	}

	rows := mapRows(decoded)
	table := output.Table{
		Headers: []string{"KEY", "VALUE"},
		Rows:    rows,
	}
	return table.Render(os.Stdout)
}

func mapRows(value any) [][]string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		rows := make([][]string, 0, len(keys))
		for _, key := range keys {
			rows = append(rows, []string{key, formatJSONValue(typed[key])})
		}
		return rows
	case []any:
		rows := make([][]string, 0, len(typed))
		for index, item := range typed {
			rows = append(rows, []string{strconv.Itoa(index), formatJSONValue(item)})
		}
		return rows
	default:
		return [][]string{{"value", formatJSONValue(typed)}}
	}
}

func formatJSONValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

func appendTrailingNewline(payload []byte) []byte {
	if len(payload) == 0 {
		return []byte("\n")
	}
	if payload[len(payload)-1] == '\n' {
		return payload
	}
	return append(payload, '\n')
}

func init() {
	rootCmd.AddCommand(newAppProxyCmd())
}
