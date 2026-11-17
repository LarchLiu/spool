package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
)

// ReadCodexToolImages recovers pixels omitted by app-server's imageView item.
// Only the requested call's output is decoded; other tool bodies stay on disk.
func ReadCodexToolImages(ctx context.Context, path, turnID, callID string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	needle, _ := json.Marshal(callID)
	reader := bufio.NewReaderSize(io.NewSectionReader(file, 0, info.Size()), 64*1024)
	var edge []byte
	var start, position int64
	currentTurn, candidate := "", false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fragment, readErr := reader.ReadSlice('\n')
		bridge := append(append([]byte{}, edge...), fragment[:min(len(fragment), max(len(needle), 16))]...)
		candidate = candidate || bytes.Contains(fragment, needle) || bytes.Contains(bridge, needle) || codexMetadataCandidate(fragment) || codexMetadataCandidate(bridge)
		edge = append(edge[:0], fragment[max(0, len(fragment)-max(len(needle), 16)):]...)
		position += int64(len(fragment))
		if readErr == bufio.ErrBufferFull {
			continue
		}
		if candidate {
			var record struct {
				Type    string `json:"type"`
				Payload struct {
					Type   string          `json:"type"`
					TurnID string          `json:"turn_id"`
					CallID string          `json:"call_id"`
					Output json.RawMessage `json:"output"`
				} `json:"payload"`
			}
			// A single image is bounded by the same 16 MiB decoded budget as
			// the image endpoint. Leave room for its base64 and record metadata.
			const maxRecordBytes = 32 * 1024 * 1024
			if position-start <= maxRecordBytes && json.NewDecoder(io.NewSectionReader(file, start, position-start)).Decode(&record) == nil {
				payload := record.Payload
				if payload.TurnID != "" && (record.Type == "turn_context" || record.Type == "event_msg" && payload.Type == "task_started") {
					currentTurn = payload.TurnID
				}
				if currentTurn == turnID && record.Type == "response_item" && payload.CallID == callID && (payload.Type == "function_call_output" || payload.Type == "custom_tool_call_output") {
					var output any
					if err := json.Unmarshal(payload.Output, &output); err != nil {
						return nil, err
					}
					return codexToolImages(output), nil
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil, nil
			}
			return nil, readErr
		}
		start, candidate, edge = position, false, edge[:0]
	}
}
