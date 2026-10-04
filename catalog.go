package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Signal describes one CAN signal within a frame.
type Signal struct {
	Name     string  `json:"name"`
	Start    int     `json:"start"`  // little-endian bit number, bit 0 = LSB of byte 0
	Length   int     `json:"length"` // number of bits, must be positive
	Signed   bool    `json:"signed"` // two's complement when true
	Factor   float64 `json:"factor"`
	Offset   float64 `json:"offset"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	HasRange bool    `json:"-"`
}

// Mux describes an optional unsigned selector signal and its branches.
type Mux struct {
	Name     string
	Start    int
	Length   int
	Branches map[int][]*Signal
}

// Frame describes one CAN frame layout.
type Frame struct {
	ID      uint32
	Name    string
	DLC     int // payload byte count, 1..8
	Signals []*Signal
	Mux     *Mux
}

// Catalog is the decoded signal directory.
type Catalog struct {
	Frames []*Frame
	byID   map[uint32]*Frame
}

// FrameByID returns the frame definition for an 11-bit identifier.
func (c *Catalog) FrameByID(id uint32) *Frame { return c.byID[id] }

type rawSignal struct {
	Name   string   `json:"name"`
	Start  int      `json:"start"`
	Length int      `json:"length"`
	Signed bool     `json:"signed"`
	Factor *float64 `json:"factor"`
	Offset *float64 `json:"offset"`
	Min    *float64 `json:"min"`
	Max    *float64 `json:"max"`
}

type rawMux struct {
	Name     string                 `json:"name"`
	Start    int                    `json:"start"`
	Length   int                    `json:"length"`
	Branches map[string][]rawSignal `json:"branches"`
}

type rawFrame struct {
	ID      uint32      `json:"id"`
	Name    string      `json:"name"`
	DLC     int         `json:"dlc"`
	Signals []rawSignal `json:"signals"`
	Mux     *rawMux     `json:"mux"`
}

type rawCatalog struct {
	Frames []rawFrame `json:"frames"`
}

func (s *Signal) bitEnd() int { return s.Start + s.Length - 1 }

func convertSignal(rs rawSignal, where string) (*Signal, error) {
	if rs.Name == "" {
		return nil, fmt.Errorf("%s: signal name must not be empty", where)
	}
	if rs.Length <= 0 {
		return nil, fmt.Errorf("%s: signal %q: length must be positive", where, rs.Name)
	}
	if rs.Start < 0 {
		return nil, fmt.Errorf("%s: signal %q: start must be non-negative", where, rs.Name)
	}
	sig := &Signal{
		Name:   rs.Name,
		Start:  rs.Start,
		Length: rs.Length,
		Signed: rs.Signed,
		Factor: 1.0,
	}
	if rs.Factor != nil {
		sig.Factor = *rs.Factor
	}
	if rs.Offset != nil {
		sig.Offset = *rs.Offset
	}
	if rs.Min != nil || rs.Max != nil {
		if rs.Min == nil || rs.Max == nil {
			return nil, fmt.Errorf("%s: signal %q: min and max must be given together", where, rs.Name)
		}
		if *rs.Min > *rs.Max {
			return nil, fmt.Errorf("%s: signal %q: min exceeds max", where, rs.Name)
		}
		sig.Min, sig.Max, sig.HasRange = *rs.Min, *rs.Max, true
	}
	return sig, nil
}

func checkOverlap(segments map[string][2]int, name string, start, length int) error {
	end := start + length - 1
	for other, seg := range segments {
		if start <= seg[1] && seg[0] <= end {
			return fmt.Errorf("signal %q overlaps signal %q", name, other)
		}
	}
	segments[name] = [2]int{start, end}
	return nil
}

func buildCatalog(rc rawCatalog) (*Catalog, error) {
	cat := &Catalog{byID: make(map[uint32]*Frame)}
	for i, rf := range rc.Frames {
		where := fmt.Sprintf("frame[%d]", i)
		if rf.DLC < 1 || rf.DLC > 8 {
			return nil, fmt.Errorf("%s: dlc must be between 1 and 8, got %d", where, rf.DLC)
		}
		if rf.ID > 0x7FF {
			return nil, fmt.Errorf("%s: id 0x%X exceeds 11-bit standard range", where, rf.ID)
		}
		if _, dup := cat.byID[rf.ID]; dup {
			return nil, fmt.Errorf("%s: duplicate frame id 0x%X", where, rf.ID)
		}
		frame := &Frame{ID: rf.ID, Name: rf.Name, DLC: rf.DLC}
		totalBits := rf.DLC * 8
		names := map[string]bool{}
		segments := map[string][2]int{}

		validate := func(rs rawSignal) (*Signal, error) {
			sig, err := convertSignal(rs, where)
			if err != nil {
				return nil, err
			}
			if names[sig.Name] {
				return nil, fmt.Errorf("%s: duplicate signal name %q", where, sig.Name)
			}
			names[sig.Name] = true
			if sig.bitEnd() >= totalBits {
				return nil, fmt.Errorf("%s: signal %q: bits %d..%d exceed payload of %d bytes",
					where, sig.Name, sig.Start, sig.bitEnd(), rf.DLC)
			}
			return sig, nil
		}

		for _, rs := range rf.Signals {
			sig, err := validate(rs)
			if err != nil {
				return nil, err
			}
			if err := checkOverlap(segments, sig.Name, sig.Start, sig.Length); err != nil {
				return nil, fmt.Errorf("%s: %v", where, err)
			}
			frame.Signals = append(frame.Signals, sig)
		}

		if rf.Mux != nil {
			m := rf.Mux
			mux := &Mux{Name: m.Name, Start: m.Start, Length: m.Length, Branches: map[int][]*Signal{}}
			if mux.Name == "" {
				return nil, fmt.Errorf("%s: mux name must not be empty", where)
			}
			if mux.Length <= 0 || mux.Start < 0 {
				return nil, fmt.Errorf("%s: mux %q: invalid start/length", where, mux.Name)
			}
			if names[mux.Name] {
				return nil, fmt.Errorf("%s: duplicate signal name %q", where, mux.Name)
			}
			names[mux.Name] = true
			if mux.Start+mux.Length-1 >= totalBits {
				return nil, fmt.Errorf("%s: mux %q exceeds payload", where, mux.Name)
			}
			if err := checkOverlap(segments, mux.Name, mux.Start, mux.Length); err != nil {
				return nil, fmt.Errorf("%s: %v", where, err)
			}
			for key, rss := range m.Branches {
				var branchVal int
				if _, err := fmt.Sscanf(key, "%d", &branchVal); err != nil {
					return nil, fmt.Errorf("%s: invalid branch key %q", where, key)
				}
				if branchVal < 0 || branchVal >= 1<<uint(mux.Length) {
					return nil, fmt.Errorf("%s: branch %d not representable in %d bits", where, branchVal, mux.Length)
				}
				if _, dup := mux.Branches[branchVal]; dup {
					return nil, fmt.Errorf("%s: duplicate branch %d", where, branchVal)
				}
				branchSegs := map[string][2]int{}
				for other, seg := range segments {
					branchSegs[other] = seg
				}
				var sigs []*Signal
				for _, rs := range rss {
					sig, err := validate(rs)
					if err != nil {
						return nil, err
					}
					if err := checkOverlap(branchSegs, sig.Name, sig.Start, sig.Length); err != nil {
						return nil, fmt.Errorf("%s: branch %d: %v", where, branchVal, err)
					}
					sigs = append(sigs, sig)
				}
				mux.Branches[branchVal] = sigs
			}
			frame.Mux = mux
		}

		cat.Frames = append(cat.Frames, frame)
		cat.byID[frame.ID] = frame
	}
	return cat, nil
}

// LoadCatalog reads and validates a signal catalog JSON file.
func LoadCatalog(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rc rawCatalog
	if err := json.Unmarshal(data, &rc); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	return buildCatalog(rc)
}
