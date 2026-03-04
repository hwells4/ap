package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// ReadAll reads all events from an events.jsonl file.
// Returns an empty slice (not an error) if the file does not exist.
func ReadAll(path string) ([]Event, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("events: open %s: %w", path, err)
	}
	defer file.Close()

	var out []Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // up to 1MB lines
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			return nil, fmt.Errorf("events: parse line: %w", err)
		}
		out = append(out, evt)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("events: scan %s: %w", path, err)
	}
	return out, nil
}
