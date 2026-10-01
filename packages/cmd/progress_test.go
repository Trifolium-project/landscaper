package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"
)

func TestJSONProgressEmitter(t *testing.T) {
	mode := "json"
	id := "test-run"
	oldMode, oldID := progressMode, runID
	oldDone, oldTotal, oldCommand := progressDone, progressTotal, progressCommand
	oldStderr := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Stderr = oldStderr
		progressMode, runID = oldMode, oldID
		progressDone, progressTotal, progressCommand = oldDone, oldTotal, oldCommand
		reader.Close()
		writer.Close()
	})
	os.Stderr = writer
	progressMode, runID = &mode, &id
	progressDone, progressTotal, progressCommand = 0, 2, "artifact download"
	emitProgress(map[string]interface{}{"package": "PKG", "artifact": "ONE", "status": "ok"})
	emitProgress(map[string]interface{}{"package": "PKG", "artifact": "TWO", "status": "failed", "failed": true})
	writer.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var lines []map[string]interface{}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var item map[string]interface{}
		if err := json.Unmarshal(line, &item); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, item)
	}
	if len(lines) != 2 || lines[0]["done"] != float64(1) || lines[1]["done"] != float64(2) {
		t.Fatalf("unexpected progress sequence: %v", lines)
	}
	if lines[0]["total"] != float64(2) || lines[1]["total"] != float64(2) {
		t.Fatalf("total changed: %v", lines)
	}
	if lines[1]["status"] != "failed" || lines[1]["item"] != "PKG/TWO" || lines[1]["run_id"] != id {
		t.Fatalf("unexpected final progress: %v", lines[1])
	}
}
