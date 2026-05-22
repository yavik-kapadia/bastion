package logging

import (
	"encoding/json"

	"github.com/yavik-kapadia/bastion/internal/db"
)

// HubBroadcaster bridges the logging package to the WebSocket hub without
// depending on the ws package directly. The bound function is typically
// hub.Broadcast.
type HubBroadcaster struct {
	// Send is invoked once per JSON-encoded message.
	Send func([]byte)
}

// LogBatchMessage is the WS envelope for a flushed batch of records.
// The client filters by `type` ("logs") and the optional `stream` field.
type LogBatchMessage struct {
	Type    string         `json:"type"`
	Records []recordOnWire `json:"records"`
}

type recordOnWire struct {
	ID     int64          `json:"id"`
	TS     int64          `json:"ts"`
	Level  string         `json:"level"`
	Stream *string        `json:"stream"`
	Msg    string         `json:"msg"`
	Attrs  map[string]any `json:"attrs"`
}

// BroadcastLogs encodes the batch and pushes it to the hub.
func (b HubBroadcaster) BroadcastLogs(records []db.EventLog) {
	if b.Send == nil || len(records) == 0 {
		return
	}
	msg := LogBatchMessage{
		Type:    "logs",
		Records: make([]recordOnWire, 0, len(records)),
	}
	for _, e := range records {
		var stream *string
		if e.Stream != "" {
			s := e.Stream
			stream = &s
		}
		attrs := map[string]any{}
		if e.Attrs != "" {
			_ = json.Unmarshal([]byte(e.Attrs), &attrs)
		}
		msg.Records = append(msg.Records, recordOnWire{
			ID:     e.ID,
			TS:     e.TS,
			Level:  e.Level,
			Stream: stream,
			Msg:    e.Msg,
			Attrs:  attrs,
		})
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	b.Send(data)
}
