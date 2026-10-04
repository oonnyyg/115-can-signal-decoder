package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// inputRecord is one JSONL log line. ID accepts a number or "0x.." string.
type inputRecord struct {
	ID   json.RawMessage `json:"id"`
	Data string          `json:"data"`
}

func parseID(raw json.RawMessage) (uint32, error) {
	s := strings.TrimSpace(string(raw))
	if len(s) == 0 {
		return 0, fmt.Errorf("missing id")
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return 0, fmt.Errorf("invalid id: %v", err)
		}
		str = strings.TrimPrefix(strings.ToLower(str), "0x")
		var v uint64
		if _, err := fmt.Sscanf(str, "%x", &v); err != nil {
			return 0, fmt.Errorf("invalid id %q", str)
		}
		return uint32(v), nil
	}
	var v uint32
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, fmt.Errorf("invalid id: %v", err)
	}
	return v, nil
}

type signalStats struct {
	Count      int `json:"count"`
	OutOfRange int `json:"out_of_range"`
}

// Summary aggregates decoding statistics across all records.
type Summary struct {
	Records  int                    `json:"records"`
	Decoded  int                    `json:"decoded"`
	Errors   int                    `json:"errors"`
	Signals  map[string]signalStats `json:"signals"`
	Min      float64                `json:"min"`
	Max      float64                `json:"max"`
	hasValue bool
}

func (s *Summary) add(df DecodedFrame) {
	s.Decoded++
	for _, ds := range df.Signals {
		st := s.Signals[ds.Name]
		st.Count++
		if ds.OutOfRange {
			st.OutOfRange++
		}
		s.Signals[ds.Name] = st
		if !s.hasValue {
			s.Min, s.Max, s.hasValue = ds.Value, ds.Value, true
		} else {
			if ds.Value < s.Min {
				s.Min = ds.Value
			}
			if ds.Value > s.Max {
				s.Max = ds.Value
			}
		}
	}
}

type errorRecord struct {
	Line  int    `json:"line"`
	Error string `json:"error"`
}

// Process reads JSONL records from r, writes decoded JSONL to out and
// per-line errors to errOut, returning the aggregate summary.
func Process(cat *Catalog, r io.Reader, out, errOut io.Writer) (*Summary, error) {
	sum := &Summary{Signals: map[string]signalStats{}}
	enc := json.NewEncoder(out)
	errEnc := json.NewEncoder(errOut)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		sum.Records++
		fail := func(msg string) {
			sum.Errors++
			errEnc.Encode(errorRecord{Line: line, Error: msg})
		}
		var rec inputRecord
		if err := json.Unmarshal([]byte(text), &rec); err != nil {
			fail(fmt.Sprintf("invalid JSON: %v", err))
			continue
		}
		id, err := parseID(rec.ID)
		if err != nil {
			fail(err.Error())
			continue
		}
		frame := cat.FrameByID(id)
		if frame == nil {
			fail(fmt.Sprintf("unknown frame id 0x%X", id))
			continue
		}
		payload, err := hex.DecodeString(strings.TrimSpace(rec.Data))
		if err != nil {
			fail(fmt.Sprintf("invalid hex payload: %v", err))
			continue
		}
		if len(payload) != frame.DLC {
			fail(fmt.Sprintf("payload length %d does not match dlc %d", len(payload), frame.DLC))
			continue
		}
		df := decodeFrame(line, frame, payload)
		sum.add(df)
		if err := enc.Encode(df); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return sum, nil
}
