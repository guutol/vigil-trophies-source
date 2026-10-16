package notify

import (
	"encoding/binary"
	"errors"
	"sync"

	"github.com/vigil-tracker/agent/internal/config"
)

// VolumeFactor is how loud each level plays the (already quiet) sounds.
func VolumeFactor(volume string) float64 {
	switch volume {
	case config.VolumeLow:
		return 0.4
	case config.VolumeMedium:
		return 0.7
	case config.VolumeHigh:
		return 1.0
	}
	return 0
}

// ScaleWAV returns a copy of a 16-bit PCM WAV with its samples multiplied by factor (0..1).
func ScaleWAV(wav []byte, factor float64) ([]byte, error) {
	if len(wav) < 12 || string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	out := append([]byte(nil), wav...)
	bits := 0
	for pos := 12; pos+8 <= len(out); {
		id := string(out[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(out[pos+4 : pos+8]))
		body := pos + 8
		if size < 0 || body+size > len(out) {
			return nil, errors.New("broken WAV chunk")
		}
		switch id {
		case "fmt ":
			if size < 16 || binary.LittleEndian.Uint16(out[body:]) != 1 {
				return nil, errors.New("only PCM WAV is supported")
			}
			bits = int(binary.LittleEndian.Uint16(out[body+14:]))
		case "data":
			if bits != 16 {
				return nil, errors.New("only 16-bit WAV is supported")
			}
			for i := body; i+1 < body+size; i += 2 {
				v := float64(int16(binary.LittleEndian.Uint16(out[i:]))) * factor
				binary.LittleEndian.PutUint16(out[i:], uint16(int16(max(-32768, min(32767, v)))))
			}
			return out, nil
		}
		pos = body + size + size%2
	}
	return nil, errors.New("WAV without data")
}

var (
	soundsMu sync.Mutex
	sounds   = map[[2]string][]byte{}
)

// Sound is the chime (or the platinum one) at a volume level; nil for "off". The bytes are kept
// for the life of the process (PlaySound reads them asynchronously).
func Sound(platinum bool, volume string) []byte {
	f := VolumeFactor(volume)
	if f <= 0 {
		return nil
	}
	kind := "chime"
	src := ChimeWAV
	if platinum {
		kind, src = "platinum", PlatinumWAV
	}
	key := [2]string{kind, volume}
	soundsMu.Lock()
	defer soundsMu.Unlock()
	if s, ok := sounds[key]; ok {
		return s
	}
	s, err := ScaleWAV(src, f)
	if err != nil {
		return nil
	}
	sounds[key] = s
	return s
}
