package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("serialctl", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", "http://localhost:8080", "server URL")
	if err := flags.Parse(args); err != nil {
		return err
	}

	rest := flags.Args()
	if len(rest) < 2 {
		return usageError()
	}

	client := &http.Client{}
	switch rest[0] {
	case "hosts":
		if rest[1] != "list" || len(rest) != 2 {
			return usageError()
		}
		return getJSON(client, *server, "/api/agents", stdout)
	case "channels":
		if rest[1] != "list" || len(rest) != 2 {
			return usageError()
		}
		return getJSON(client, *server, "/api/channels", stdout)
	case "rfc2217":
		if rest[1] != "list" || len(rest) != 2 {
			return usageError()
		}
		return getJSON(client, *server, "/api/channels", stdout)
	case "logs":
		if rest[1] != "download" {
			return usageError()
		}
		return downloadLogs(client, *server, rest[2:], stdout)
	default:
		return usageError()
	}
}

func getJSON(client *http.Client, serverURL string, path string, stdout io.Writer) error {
	body, err := get(client, serverURL, path, nil)
	if err != nil {
		return err
	}

	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func downloadLogs(client *http.Client, serverURL string, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("logs download", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	channelID := flags.String("channel-id", "", "channel ID")
	from := flags.String("from", "", "start time")
	to := flags.String("to", "", "end time")
	format := flags.String("format", "", "text or raw")
	direction := flags.String("direction", "", "rx, tx, or both")
	timestamp := flags.Bool("timestamp", false, "include timestamps")
	directionLabel := flags.Bool("direction-label", false, "include direction labels")
	stripANSI := flags.Bool("strip-ansi", false, "strip ANSI escape sequences")
	output := flags.String("output", "", "output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *channelID == "" {
		return fmt.Errorf("--channel-id is required")
	}
	if flags.NArg() != 0 {
		return usageError()
	}

	query := url.Values{}
	query.Set("channel_id", *channelID)
	setQueryIfNotEmpty(query, "from", *from)
	setQueryIfNotEmpty(query, "to", *to)
	setQueryIfNotEmpty(query, "format", *format)
	setQueryIfNotEmpty(query, "direction", *direction)
	if *timestamp {
		query.Set("timestamp", "true")
	}
	if *directionLabel {
		query.Set("direction_label", "true")
	}
	if *stripANSI {
		query.Set("strip_ansi", "true")
	}

	body, err := get(client, serverURL, "/api/logs/download", query)
	if err != nil {
		return err
	}
	if *output == "" {
		_, err = stdout.Write(body)
		return err
	}
	return os.WriteFile(*output, body, 0o644)
}

func setQueryIfNotEmpty(query url.Values, key string, value string) {
	if value != "" {
		query.Set(key, value)
	}
}

func get(client *http.Client, serverURL string, path string, query url.Values) ([]byte, error) {
	reqURL, err := url.JoinPath(strings.TrimRight(serverURL, "/"), path)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(reqURL)
	if err != nil {
		return nil, err
	}
	if len(query) > 0 {
		parsed.RawQuery = query.Encode()
	}

	resp, err := client.Get(parsed.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s returned %s: %s", parsed.String(), resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func usageError() error {
	return fmt.Errorf("usage: serialctl --server URL hosts list | channels list | rfc2217 list | logs download --channel-id ID [--from TIME] [--to TIME] [--output PATH]")
}
