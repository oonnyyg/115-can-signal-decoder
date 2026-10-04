package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Signal describes one decoded quantity within a frame.
type Signal struct {
	Name   string  `json:"name"`
	Start  int     `json:"start"`
	Length int     `json:"length"`
	Signed bool    `json:"signed"`
	Factor float64 `json:"factor"`
	Offset float64 `json:"offset"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	HasRng bool    `json:"-"`
}

// signalJSON mirrors Signal but tracks whether a range was declared.
type signalJSON struct {
	Name   string    `json:"name"`
	Start  int       `json:"start"`
	Length int       `json:"length"`
	Signed bool      `json:"signed"`
	Factor *float64  `json:"factor"`
	Offset *float64  `json:"offset"`
	Range  []float64 `json:"range"`
}

// Mux is the optional unsigned selector signal of a frame.
type Mux struct {
	Name   string `json:"name"`
	Start  int    `json:"start"`
	Length int    `json:"length"`
}

// Branch holds signals active only when the mux raw value matches Value.
type Branch struct {
	Value   uint64   `json:"value"`
	Signals []Signal `json:"signals"`
}

// Frame describes one CAN standard frame layout.
type Frame struct {
	ID       uint32   `json:"id"`
	Name     string   `json:"name"`
	DLC      int      `json:"dlc"`
	Signals  []Signal `json:"signals"`
	Mux      *Mux     `json:"mux"`
	Branches []Branch `json:"branches"`
}

type frameJSON struct {
	ID       uint32       `json:"id"`
	Name     string       `json:"name"`
	DLC      int          `json:"dlc"`
	Signals  []signalJSON `json:"signals"`
	Mux      *Mux         `json:"mux"`
	Branches []branchJSON `json:"branches"`
}

type branchJSON struct {
	Value   uint64       `json:"value"`
	Signals []signalJSON `json:"signals"`
}

type catalogJSON struct {
	Frames []frameJSON `json:"frames"`
}

// Catalog maps frame IDs to validated frame layouts.
type Catalog struct {
	Frames map[uint32]*Frame
}

func convertSignal(s signalJSON, ctx string) (Signal, error) {
	sig := Signal{
		Name:   s.Name,
		Start:  s.Start,
		Length: s.Length,
		Signed: s.Signed,
		Factor: 1,
	}
	if s.Name == "" {
		return sig, fmt.Errorf("%s: signal name must not be empty", ctx)
	}
	if s.Factor != nil {
		sig.Factor = *s.Factor
	}
	if s.Offset != nil {
		sig.Offset = *s.Offset
	}
	if s.Range != nil {
		if len(s.Range) != 2 || s.Range[0] > s.Range[1] {
			return sig, fmt.Errorf("%s: signal %q: range must be [min,max] with min<=max", ctx, s.Name)
		}
		sig.Min, sig.Max, sig.HasRng = s.Range[0], s.Range[1], true
	}
	return sig, nil
}

// LoadCatalog parses and validates a catalog JSON file.
func LoadCatalog(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw catalogJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	cat := &Catalog{Frames: map[uint32]*Frame{}}
	for i, fj := range raw.Frames {
		ctx := fmt.Sprintf("frame[%d] id=0x%X", i, fj.ID)
		if fj.ID > 0x7FF {
			return nil, fmt.Errorf("%s: id exceeds 11-bit standard range", ctx)
		}
		if fj.DLC < 1 || fj.DLC > 8 {
			return nil, fmt.Errorf("%s: dlc must be 1..8, got %d", ctx, fj.DLC)
		}
		if _, dup := cat.Frames[fj.ID]; dup {
			return nil, fmt.Errorf("%s: duplicate frame id", ctx)
		}
		f := &Frame{ID: fj.ID, Name: fj.Name, DLC: fj.DLC, Mux: fj.Mux}
		for _, sj := range fj.Signals {
			sig, err := convertSignal(sj, ctx)
			if err != nil {
				return nil, err
			}
			f.Signals = append(f.Signals, sig)
		}
		for _, bj := range fj.Branches {
			br := Branch{Value: bj.Value}
			for _, sj := range bj.Signals {
				sig, err := convertSignal(sj, ctx)
				if err != nil {
					return nil, err
				}
				br.Signals = append(br.Signals, sig)
			}
			f.Branches = append(f.Branches, br)
		}
		if err := validateFrame(f); err != nil {
			return nil, fmt.Errorf("%s: %w", ctx, err)
		}
		cat.Frames[f.ID] = f
	}
	return cat, nil
}

func validateFrame(f *Frame) error {
	bits := f.DLC * 8
	names := map[string]bool{}
	checkRange := func(name string, start, length int) error {
		if length <= 0 {
			return fmt.Errorf("signal %q: length must be positive", name)
		}
		if start < 0 || start+length > bits {
			return fmt.Errorf("signal %q: bits [%d,%d) exceed payload of %d bits", name, start, start+length, bits)
		}
		return nil
	}
	type seg struct {
		name       string
		start, end int
	}
	common := []seg{}
	if f.Mux != nil {
		if err := checkRange(f.Mux.Name, f.Mux.Start, f.Mux.Length); err != nil {
			return err
		}
		common = append(common, seg{f.Mux.Name, f.Mux.Start, f.Mux.Start + f.Mux.Length})
	}
	for _, s := range f.Signals {
		if err := checkRange(s.Name, s.Start, s.Length); err != nil {
			return err
		}
		if names[s.Name] {
			return fmt.Errorf("duplicate signal name %q", s.Name)
		}
		names[s.Name] = true
		common = append(common, seg{s.Name, s.Start, s.Start + s.Length})
	}
	for i := 0; i < len(common); i++ {
		for j := i + 1; j < len(common); j++ {
			if common[i].start < common[j].end && common[j].start < common[i].end {
				return fmt.Errorf("signals %q and %q overlap", common[i].name, common[j].name)
			}
		}
	}
	seenBranchVal := map[uint64]bool{}
	for _, br := range f.Branches {
		if f.Mux == nil {
			return fmt.Errorf("branches declared without a mux signal")
		}
		if seenBranchVal[br.Value] {
			return fmt.Errorf("duplicate branch value %d", br.Value)
		}
		seenBranchVal[br.Value] = true
		if br.Value >= 1<<uint(f.Mux.Length) {
			return fmt.Errorf("branch value %d does not fit mux length %d", br.Value, f.Mux.Length)
		}
		segs := append([]seg{}, common...)
		for _, s := range br.Signals {
			if err := checkRange(s.Name, s.Start, s.Length); err != nil {
				return err
			}
			if names[s.Name] {
				return fmt.Errorf("duplicate signal name %q", s.Name)
			}
			names[s.Name] = true
			segs = append(segs, seg{s.Name, s.Start, s.Start + s.Length})
		}
		sort.Slice(segs, func(a, b int) bool { return segs[a].start < segs[b].start })
		for i := 1; i < len(segs); i++ {
			if segs[i].start < segs[i-1].end {
				return fmt.Errorf("branch %d: signals %q and %q overlap", br.Value, segs[i-1].name, segs[i].name)
			}
		}
	}
	return nil
}
