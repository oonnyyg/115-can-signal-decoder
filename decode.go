package main

// extractBits reads length bits starting at bit start, little-endian bit
// numbering (bit 0 is the LSB of byte 0), crossing byte boundaries.
func extractBits(data []byte, start, length int) uint64 {
	var raw uint64
	for i := 0; i < length; i++ {
		bit := start + i
		if data[bit/8]>>(uint(bit%8))&1 == 1 {
			raw |= 1 << uint(i)
		}
	}
	return raw
}

// interpretRaw converts raw bits to a signed integer when the signal is
// declared as two's complement. Sign handling happens before scaling.
func interpretRaw(raw uint64, length int, signed bool) int64 {
	if !signed || length == 0 {
		return int64(raw)
	}
	if length >= 64 {
		return int64(raw)
	}
	if raw&(1<<uint(length-1)) != 0 {
		return int64(raw) - (1 << uint(length))
	}
	return int64(raw)
}

// DecodedSignal is one decoded signal value.
type DecodedSignal struct {
	Name       string  `json:"name"`
	Raw        int64   `json:"raw"`
	Value      float64 `json:"value"`
	OutOfRange bool    `json:"out_of_range,omitempty"`
	MuxValue   *int    `json:"mux_value,omitempty"`
}

// DecodedFrame is the traceable result for one input record.
type DecodedFrame struct {
	Line     int             `json:"line"`
	ID       uint32          `json:"id"`
	Frame    string          `json:"frame"`
	MuxKnown bool            `json:"mux_known"`
	MuxValue *int            `json:"mux_value,omitempty"`
	Signals  []DecodedSignal `json:"signals"`
}

// decodeFrame decodes a payload according to the frame definition.
// Common signals are always decoded; mux branch signals only when the
// selector value matches a declared branch.
func decodeFrame(line int, frame *Frame, data []byte) DecodedFrame {
	out := DecodedFrame{Line: line, ID: frame.ID, Frame: frame.Name, MuxKnown: true}
	decodeOne := func(sig *Signal, muxVal *int) {
		raw := extractBits(data, sig.Start, sig.Length)
		iv := interpretRaw(raw, sig.Length, sig.Signed)
		value := float64(iv)*sig.Factor + sig.Offset
		ds := DecodedSignal{Name: sig.Name, Raw: iv, Value: value, MuxValue: muxVal}
		if sig.HasRange && (value < sig.Min || value > sig.Max) {
			ds.OutOfRange = true
		}
		out.Signals = append(out.Signals, ds)
	}
	for _, sig := range frame.Signals {
		decodeOne(sig, nil)
	}
	if frame.Mux != nil {
		mv := int(extractBits(data, frame.Mux.Start, frame.Mux.Length))
		out.MuxValue = &mv
		branch, ok := frame.Mux.Branches[mv]
		if !ok {
			out.MuxKnown = false
		} else {
			for _, sig := range branch {
				decodeOne(sig, &mv)
			}
		}
	}
	return out
}
