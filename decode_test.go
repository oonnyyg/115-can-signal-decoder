package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	catJSON := `{
	  "frames": [
	    {
	      "id": 256, "name": "Status", "dlc": 8,
	      "signals": [
	        {"name": "temp", "start": 0, "length": 12, "signed": true, "factor": 0.1, "offset": -40, "min": -40, "max": 100},
	        {"name": "counter", "start": 12, "length": 4}
	      ],
	      "mux": {
	        "name": "mode", "start": 16, "length": 2,
	        "branches": {
	          "1": [{"name": "speed", "start": 24, "length": 16, "factor": 0.01, "max": 250, "min": 0}],
	          "2": [{"name": "torque", "start": 24, "length": 16, "signed": true, "factor": 0.5}]
	        }
	      }
	    }
	  ]
	}`
	var rc rawCatalog
	if err := json.Unmarshal([]byte(catJSON), &rc); err != nil {
		t.Fatal(err)
	}
	cat, err := buildCatalog(rc)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func TestNegativeSignedCrossByte(t *testing.T) {
	cat := testCatalog(t)
	// temp: 12-bit signed at bit 0, raw 0xFFE = -2 -> -2*0.1-40 = -40.2
	// counter: 4-bit at bit 12, value 0xA (crosses byte 1 into byte 2 low nibble bits)
	// bytes: byte0=0xFE, byte1 low nibble=0xF, counter bits 12..15 = 0xA -> byte1 high nibble=0xA
	// mode at bits 16..17 = 1 -> branch 1, speed raw 100 -> 1.00
	in := `{"id": 256, "data": "FEAF010001000000"}` + "\n"
	var out, errOut bytes.Buffer
	sum, err := Process(cat, strings.NewReader(in), &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected errors: %s", errOut.String())
	}
	var df DecodedFrame
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &df); err != nil {
		t.Fatal(err)
	}
	if !df.MuxKnown || df.MuxValue == nil || *df.MuxValue != 1 {
		t.Fatalf("mux: %+v", df)
	}
	got := map[string]DecodedSignal{}
	for _, s := range df.Signals {
		got[s.Name] = s
	}
	if got["temp"].Raw != -2 {
		t.Fatalf("temp raw = %d, want -2", got["temp"].Raw)
	}
	if v := got["temp"].Value; v < -40.21 || v > -40.19 {
		t.Fatalf("temp value = %v, want -40.2", v)
	}
	if got["counter"].Raw != 0xA {
		t.Fatalf("counter raw = %d, want 10", got["counter"].Raw)
	}
	if _, ok := got["speed"]; !ok {
		t.Fatal("branch signal speed missing")
	}
	if _, ok := got["torque"]; ok {
		t.Fatal("inactive branch signal torque must not appear")
	}
	if sum.Signals["temp"].Count != 1 || sum.Signals["speed"].Count != 1 {
		t.Fatalf("stats: %+v", sum.Signals)
	}
}

func TestMuxSwitchingAndUnknownBranch(t *testing.T) {
	cat := testCatalog(t)
	// mode=2 -> torque, raw 0xFFFE = -2 -> -1.0
	// mode=3 -> unknown branch, only common signals
	in := strings.Join([]string{
		`{"id": "0x100", "data": "000002FEFF000000"}`,
		`{"id": "0x100", "data": "000003FEFF000000"}`,
	}, "\n") + "\n"
	var out, errOut bytes.Buffer
	_, err := Process(cat, strings.NewReader(in), &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected errors: %s", errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 output lines, got %d", len(lines))
	}
	var first, second DecodedFrame
	json.Unmarshal([]byte(lines[0]), &first)
	json.Unmarshal([]byte(lines[1]), &second)
	if !first.MuxKnown || *first.MuxValue != 2 {
		t.Fatalf("first mux: %+v", first)
	}
	names := map[string]bool{}
	for _, s := range first.Signals {
		names[s.Name] = true
		if s.Name == "torque" {
			if s.Raw != -2 || s.Value != -1.0 {
				t.Fatalf("torque: %+v", s)
			}
		}
	}
	if !names["torque"] || names["speed"] {
		t.Fatalf("branch 2 signals wrong: %v", names)
	}
	if second.MuxKnown {
		t.Fatal("second record must report unknown mux branch")
	}
	for _, s := range second.Signals {
		if s.Name == "torque" || s.Name == "speed" {
			t.Fatalf("unknown branch must not decode branch signals: %+v", s)
		}
	}
	if len(second.Signals) != 2 {
		t.Fatalf("unknown branch should still emit 2 common signals, got %d", len(second.Signals))
	}
}

func TestErrorsDoNotBlock(t *testing.T) {
	cat := testCatalog(t)
	in := strings.Join([]string{
		`{"id": 999, "data": "0000000000000000"}`,
		`{"id": 256, "data": "ZZ"}`,
		`{"id": 256, "data": "00"}`,
		`{"id": 256, "data": "0000000000000000"}`,
	}, "\n") + "\n"
	var out, errOut bytes.Buffer
	sum, err := Process(cat, strings.NewReader(in), &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Errors != 3 || sum.Decoded != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	errLines := strings.Split(strings.TrimSpace(errOut.String()), "\n")
	if len(errLines) != 3 {
		t.Fatalf("want 3 error lines, got %d: %s", len(errLines), errOut.String())
	}
	var e1, e2, e3 errorRecord
	json.Unmarshal([]byte(errLines[0]), &e1)
	json.Unmarshal([]byte(errLines[1]), &e2)
	json.Unmarshal([]byte(errLines[2]), &e3)
	if e1.Line != 1 || !strings.Contains(e1.Error, "unknown frame") {
		t.Fatalf("e1: %+v", e1)
	}
	if e2.Line != 2 || !strings.Contains(e2.Error, "hex") {
		t.Fatalf("e2: %+v", e2)
	}
	if e3.Line != 3 || !strings.Contains(e3.Error, "length") {
		t.Fatalf("e3: %+v", e3)
	}
}

func TestOutOfRangeMarkedNotDropped(t *testing.T) {
	cat := testCatalog(t)
	// temp raw 0x7FF = 2047 -> 204.7 - 40 = 164.7 > max 100
	in := `{"id": 256, "data": "FF07000000000000"}` + "\n"
	var out, errOut bytes.Buffer
	sum, err := Process(cat, strings.NewReader(in), &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	var df DecodedFrame
	json.Unmarshal(bytes.TrimSpace(out.Bytes()), &df)
	for _, s := range df.Signals {
		if s.Name == "temp" {
			if !s.OutOfRange {
				t.Fatal("temp should be marked out of range")
			}
			if s.Value < 164.6 || s.Value > 164.8 {
				t.Fatalf("raw value must be kept, got %v", s.Value)
			}
		}
	}
	if sum.Signals["temp"].OutOfRange != 1 {
		t.Fatalf("stats: %+v", sum.Signals)
	}
	if sum.Max < 164.6 {
		t.Fatalf("summary max should include out-of-range value: %+v", sum)
	}
}

func TestCatalogValidation(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"duplicate id", `{"frames":[{"id":1,"dlc":8},{"id":1,"dlc":8}]}`},
		{"extended id", `{"frames":[{"id":2048,"dlc":8}]}`},
		{"bad dlc", `{"frames":[{"id":1,"dlc":9}]}`},
		{"zero length", `{"frames":[{"id":1,"dlc":8,"signals":[{"name":"a","start":0,"length":0}]}]}`},
		{"out of payload", `{"frames":[{"id":1,"dlc":1,"signals":[{"name":"a","start":7,"length":2}]}]}`},
		{"dup signal name", `{"frames":[{"id":1,"dlc":8,"signals":[{"name":"a","start":0,"length":1},{"name":"a","start":2,"length":1}]}]}`},
		{"common overlap", `{"frames":[{"id":1,"dlc":8,"signals":[{"name":"a","start":0,"length":4},{"name":"b","start":2,"length":4}]}]}`},
		{"mux overlap common", `{"frames":[{"id":1,"dlc":8,"signals":[{"name":"a","start":0,"length":4}],"mux":{"name":"m","start":2,"length":2,"branches":{}}}]}`},
		{"branch overlap common", `{"frames":[{"id":1,"dlc":8,"signals":[{"name":"a","start":0,"length":4}],"mux":{"name":"m","start":8,"length":2,"branches":{"1":[{"name":"b","start":1,"length":4}]}}}]}`},
		{"branch overlap mux", `{"frames":[{"id":1,"dlc":8,"mux":{"name":"m","start":8,"length":2,"branches":{"1":[{"name":"b","start":8,"length":4}]}}}]}`},
		{"branch key out of range", `{"frames":[{"id":1,"dlc":8,"mux":{"name":"m","start":8,"length":2,"branches":{"4":[{"name":"b","start":16,"length":4}]}}}]}`},
		{"branch overlap same branch", `{"frames":[{"id":1,"dlc":8,"mux":{"name":"m","start":8,"length":2,"branches":{"1":[{"name":"b","start":16,"length":4},{"name":"c","start":18,"length":4}]}}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rc rawCatalog
			if err := json.Unmarshal([]byte(tc.json), &rc); err != nil {
				t.Fatal(err)
			}
			if _, err := buildCatalog(rc); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestBranchesMayReuseBits(t *testing.T) {
	catJSON := `{"frames":[{"id":1,"dlc":8,"mux":{"name":"m","start":0,"length":1,
	  "branches":{
	    "0":[{"name":"a","start":8,"length":8}],
	    "1":[{"name":"b","start":8,"length":8}]
	  }}}]}`
	var rc rawCatalog
	if err := json.Unmarshal([]byte(catJSON), &rc); err != nil {
		t.Fatal(err)
	}
	if _, err := buildCatalog(rc); err != nil {
		t.Fatalf("different branches must be allowed to reuse bits: %v", err)
	}
}
