package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Message is one input JSONL record.
type Message struct {
	ID   uint32 `json:"id"`
	Data string `json:"data"`
}

// OutputRecord is one output JSONL record (decoded result or error).
type OutputRecord struct {
	Line       int            `json:"line"`
	ID         uint32         `json:"id,omitempty"`
	Frame      string         `json:"frame,omitempty"`
	MuxValue   *uint64        `json:"mux_value,omitempty"`
	MuxUnknown bool           `json:"mux_unknown,omitempty"`
	Signals    []DecodedValue `json:"signals,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// SignalStat accumulates per-signal statistics.
type SignalStat struct {
	Decoded    int `json:"decoded"`
	OutOfRange int `json:"out_of_range"`
}

// Summary aggregates statistics over the whole run.
type Summary struct {
	Records int                    `json:"records"`
	Decoded int                    `json:"decoded"`
	Errors  int                    `json:"errors"`
	Signals map[string]*SignalStat `json:"signals"`
	Min     *float64               `json:"min,omitempty"`
	Max     *float64               `json:"max,omitempty"`
}

func newSummary() *Summary {
	return &Summary{Signals: map[string]*SignalStat{}}
}

func (s *Summary) observe(v DecodedValue) {
	st := s.Signals[v.Name]
	if st == nil {
		st = &SignalStat{}
		s.Signals[v.Name] = st
	}
	st.Decoded++
	if v.OutOfRange {
		st.OutOfRange++
	}
	if s.Min == nil || v.Value < *s.Min {
		m := v.Value
		s.Min = &m
	}
	if s.Max == nil || v.Value > *s.Max {
		m := v.Value
		s.Max = &m
	}
}

// Process reads JSONL messages from r, decodes them against the catalog and
// writes one JSONL record per input line to out, preserving input order.
// The summary is written as the final line. Bad records never stop the run.
func Process(cat *Catalog, r io.Reader, out io.Writer) (*Summary, error) {
	sum := newSummary()
	enc := json.NewEncoder(out)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	line := 0
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		line++
		sum.Records++
		rec := OutputRecord{Line: line}
		var msg Message
		if err := json.Unmarshal([]byte(text), &msg); err != nil {
			rec.Error = fmt.Sprintf("invalid JSON: %v", err)
			sum.Errors++
			if err := enc.Encode(rec); err != nil {
				return nil, err
			}
			continue
		}
		rec.ID = msg.ID
		frame, ok := cat.Frames[msg.ID]
		if !ok {
			rec.Error = fmt.Sprintf("unknown frame id 0x%X", msg.ID)
			sum.Errors++
			if err := enc.Encode(rec); err != nil {
				return nil, err
			}
			continue
		}
		payload, err := hex.DecodeString(msg.Data)
		if err != nil {
			rec.Error = fmt.Sprintf("invalid hex data: %v", err)
			sum.Errors++
			if err := enc.Encode(rec); err != nil {
				return nil, err
			}
			continue
		}
		if len(payload) != frame.DLC {
			rec.Error = fmt.Sprintf("payload length %d does not match dlc %d", len(payload), frame.DLC)
			sum.Errors++
			if err := enc.Encode(rec); err != nil {
				return nil, err
			}
			continue
		}
		dec := DecodeFrame(frame, payload)
		rec.Frame = frame.Name
		rec.MuxValue = dec.MuxValue
		rec.MuxUnknown = dec.MuxUnknown
		rec.Signals = dec.Signals
		sum.Decoded++
		for _, v := range dec.Signals {
			sum.observe(v)
		}
		if err := enc.Encode(rec); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := enc.Encode(map[string]any{"summary": sum}); err != nil {
		return nil, err
	}
	return sum, nil
}
