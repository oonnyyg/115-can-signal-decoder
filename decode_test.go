package main

import (
	"os"
	"strings"
	"testing"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	catJSON := `{
	  "frames": [
	    {
	      "id": 256,
	      "name": "Status",
	      "dlc": 8,
	      "signals": [
	        {"name": "temp", "start": 0, "length": 12, "signed": true, "factor": 0.1, "offset": -40, "range": [-40, 100]},
	        {"name": "speed", "start": 12, "length": 10, "factor": 0.5}
	      ],
	      "mux": {"name": "mode", "start": 22, "length": 2},
	      "branches": [
	        {"value": 0, "signals": [{"name": "torque", "start": 24, "length": 8, "signed": true}]},
	        {"value": 1, "signals": [{"name": "level", "start": 24, "length": 8, "factor": 0.5}]}
	      ]
	    }
	  ]
	}`
	path := writeTemp(t, "catalog.json", catJSON)
	cat, err := LoadCatalog(path)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	return cat
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := dir + "/" + name
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return p
}

func TestDecodeNegativeCrossByte(t *testing.T) {
	cat := testCatalog(t)
	f := cat.Frames[256]
	// temp: 12-bit signed at bit 0, raw 0xFFE = -2 -> -2*0.1-40 = -40.2
	// crosses byte boundary (bits 0..11). mode=0 here, so torque also decodes.
	data := []byte{0xFE, 0x0F, 0x00, 0, 0, 0, 0, 0}
	dec := DecodeFrame(f, data)
	if len(dec.Signals) != 3 {
		t.Fatalf("expected 3 signals, got %d", len(dec.Signals))
	}
	got := dec.Signals[0]
	if got.Name != "temp" || got.Raw != -2 {
		t.Fatalf("temp raw = %d, want -2", got.Raw)
	}
	if diff := got.Value - (-40.2); diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("temp value = %v, want -40.2", got.Value)
	}
	if !got.OutOfRange {
		t.Fatalf("temp -40.2 should be out of range [-40,100]")
	}
}

func TestMuxSwitchingReusesBits(t *testing.T) {
	cat := testCatalog(t)
	f := cat.Frames[256]
	// mode=1 at bits 22..23 -> byte2 bit6 = 0x40; branch 1 signal at bits 24..31 = byte3.
	data := []byte{0, 0, 0x40, 0x64, 0, 0, 0, 0}
	dec := DecodeFrame(f, data)
	if dec.MuxValue == nil || *dec.MuxValue != 1 {
		t.Fatalf("mux = %v, want 1", dec.MuxValue)
	}
	if dec.MuxUnknown {
		t.Fatalf("mux should be known")
	}
	var level *DecodedValue
	for i := range dec.Signals {
		if dec.Signals[i].Name == "level" {
			level = &dec.Signals[i]
		}
		if dec.Signals[i].Name == "torque" {
			t.Fatalf("torque must not decode when mode=1")
		}
	}
	if level == nil || level.Value != 50.0 {
		t.Fatalf("level = %+v, want 50", level)
	}
}

func TestMuxUnknownKeepsCommon(t *testing.T) {
	cat := testCatalog(t)
	f := cat.Frames[256]
	// mode=3 (no matching branch): byte2 = 0xC0.
	data := []byte{0, 0, 0xC0, 0, 0, 0, 0, 0}
	dec := DecodeFrame(f, data)
	if !dec.MuxUnknown {
		t.Fatalf("expected mux_unknown")
	}
	if len(dec.Signals) != 2 {
		t.Fatalf("expected only 2 common signals, got %d", len(dec.Signals))
	}
}

func TestProcessErrorsAndSummary(t *testing.T) {
	cat := testCatalog(t)
	input := strings.Join([]string{
		`{"id": 256, "data": "fe0f000000000000"}`,
		`{"id": 999, "data": "00"}`,
		`{"id": 256, "data": "zz"}`,
		`{"id": 256, "data": "00"}`,
	}, "\n") + "\n"
	var out strings.Builder
	sum, err := Process(cat, strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 { // 4 records + summary
		t.Fatalf("got %d output lines, want 5", len(lines))
	}
	if !strings.Contains(lines[1], "unknown frame id") {
		t.Fatalf("line 2 should report unknown frame: %s", lines[1])
	}
	if !strings.Contains(lines[2], "invalid hex") {
		t.Fatalf("line 3 should report bad hex: %s", lines[2])
	}
	if !strings.Contains(lines[3], "payload length 1 does not match dlc 8") {
		t.Fatalf("line 4 should report length mismatch: %s", lines[3])
	}
	if sum.Records != 4 || sum.Decoded != 1 || sum.Errors != 3 {
		t.Fatalf("summary counts = %+v", sum)
	}
	st := sum.Signals["temp"]
	if st == nil || st.Decoded != 1 || st.OutOfRange != 1 {
		t.Fatalf("temp stats = %+v", st)
	}
	if sum.Min == nil || *sum.Min != -40.2 {
		t.Fatalf("min = %v, want -40.2", sum.Min)
	}
	if sum.Max == nil || *sum.Max != 0.0 {
		t.Fatalf("max = %v, want 0", sum.Max)
	}
}

func TestCatalogValidation(t *testing.T) {
	cases := []struct {
		name string
		cat  string
	}{
		{"dup id", `{"frames":[{"id":1,"dlc":1},{"id":1,"dlc":1}]}`},
		{"ext id", `{"frames":[{"id":2048,"dlc":1}]}`},
		{"bad dlc", `{"frames":[{"id":1,"dlc":9}]}`},
		{"zero len", `{"frames":[{"id":1,"dlc":1,"signals":[{"name":"a","start":0,"length":0}]}]}`},
		{"out of payload", `{"frames":[{"id":1,"dlc":1,"signals":[{"name":"a","start":7,"length":2}]}]}`},
		{"dup signal", `{"frames":[{"id":1,"dlc":1,"signals":[{"name":"a","start":0,"length":1},{"name":"a","start":1,"length":1}]}]}`},
		{"common overlap", `{"frames":[{"id":1,"dlc":1,"signals":[{"name":"a","start":0,"length":2},{"name":"b","start":1,"length":2}]}]}`},
		{"branch overlaps common", `{"frames":[{"id":1,"dlc":1,"signals":[{"name":"a","start":0,"length":4}],"mux":{"name":"m","start":4,"length":2},"branches":[{"value":1,"signals":[{"name":"b","start":2,"length":2}]}]}]}`},
		{"branch overlaps mux", `{"frames":[{"id":1,"dlc":1,"mux":{"name":"m","start":0,"length":2},"branches":[{"value":1,"signals":[{"name":"b","start":1,"length":2}]}]}]}`},
		{"branch reuse ok across branches", `{"frames":[{"id":1,"dlc":1,"mux":{"name":"m","start":0,"length":1},"branches":[{"value":0,"signals":[{"name":"b","start":1,"length":2}]},{"value":1,"signals":[{"name":"c","start":1,"length":2}]}]}]}`},
	}
	for i, tc := range cases {
		p := writeTemp(t, "c.json", tc.cat)
		_, err := LoadCatalog(p)
		wantErr := tc.name != "branch reuse ok across branches"
		if wantErr && err == nil {
			t.Fatalf("case %d %s: expected error", i, tc.name)
		}
		if !wantErr && err != nil {
			t.Fatalf("case %d %s: unexpected error %v", i, tc.name, err)
		}
	}
}
