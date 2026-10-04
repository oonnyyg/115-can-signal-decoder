package main

// extractBits reads length bits starting at start, little-endian bit
// numbering: bit 0 is the least significant bit of byte 0.
func extractBits(data []byte, start, length int) uint64 {
	var v uint64
	for i := 0; i < length; i++ {
		pos := start + i
		bit := (data[pos/8] >> uint(pos%8)) & 1
		v |= uint64(bit) << uint(i)
	}
	return v
}

// interpretRaw converts the raw unsigned value to a signed integer when the
// signal is declared signed (two's complement), before any scaling.
func interpretRaw(raw uint64, length int, signed bool) int64 {
	if !signed {
		return int64(raw)
	}
	if length < 64 && raw&(1<<uint(length-1)) != 0 {
		return int64(raw) - (1 << uint(length))
	}
	return int64(raw)
}

// decodeSignal converts raw bits to a physical value: sign first, then scale.
func decodeSignal(data []byte, s Signal) (float64, int64) {
	raw := extractBits(data, s.Start, s.Length)
	iv := interpretRaw(raw, s.Length, s.Signed)
	return float64(iv)*s.Factor + s.Offset, iv
}

// DecodedValue is one decoded signal of a frame.
type DecodedValue struct {
	Name       string  `json:"name"`
	Value      float64 `json:"value"`
	Raw        int64   `json:"raw"`
	OutOfRange bool    `json:"out_of_range,omitempty"`
}

// DecodedFrame is the result of decoding one message.
type DecodedFrame struct {
	MuxValue   *uint64        `json:"mux_value,omitempty"`
	MuxUnknown bool           `json:"mux_unknown,omitempty"`
	Signals    []DecodedValue `json:"signals"`
}

// DecodeFrame decodes payload data according to the frame layout. Common
// signals are always decoded; branch signals only when the mux raw value
// matches. Unknown mux values yield common signals with MuxUnknown set.
func DecodeFrame(f *Frame, data []byte) DecodedFrame {
	out := DecodedFrame{}
	add := func(s Signal) {
		v, raw := decodeSignal(data, s)
		dv := DecodedValue{Name: s.Name, Value: v, Raw: raw}
		if s.HasRng && (v < s.Min || v > s.Max) {
			dv.OutOfRange = true
		}
		out.Signals = append(out.Signals, dv)
	}
	for _, s := range f.Signals {
		add(s)
	}
	if f.Mux != nil {
		mv := extractBits(data, f.Mux.Start, f.Mux.Length)
		out.MuxValue = &mv
		matched := false
		for _, br := range f.Branches {
			if br.Value == mv {
				matched = true
				for _, s := range br.Signals {
					add(s)
				}
			}
		}
		if !matched {
			out.MuxUnknown = true
		}
	}
	return out
}
