package rpcs3

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// TROPUSR.DAT layout, as written by RPCS3 (rpcs3/Loader/TROPUSR.h). Everything is big-endian.
//
//	header (0x30): magic u32 = 0x818F54AD, unk1 u32, tables_count u32, unk2 u32, reserved[32]
//	table headers (0x20 each): type u32, entries_size u32, unk1 u32, entries_count u32,
//	                           offset u64, reserved u64
//	type 6 entries (0x70 each; entries_size stores 0x60, the size without the 16-byte entry header):
//	    entry_type u32 = 6, entry_size u32, entry_id u32, unk u32,
//	    trophy_id u32, trophy_state u32 (1 = unlocked), unk4 u32, unk5 u32,
//	    timestamp1 u64, timestamp2 u64, unk6[64]
//
// Timestamps are CellRtcTick: microseconds since 0001-01-01 UTC. RPCS3 reads timestamp2.
const (
	tropusrMagic    = 0x818F54AD
	headerSize      = 0x30
	tableHeaderSize = 0x20
	entry6Size      = 0x70
	maxTables       = 16
	maxEntries      = 256
	// MaxFileSize is far above any real TROPUSR.DAT (a few KiB).
	MaxFileSize = 1 << 20
	// rtcEpochOffset is the CellRtcTick value of 1970-01-01T00:00:00Z.
	rtcEpochOffset = 62135596800000000
)

// Errors for unusable files. Messages never include file content.
var (
	ErrBadMagic  = errors.New("not a TROPUSR.DAT (bad magic)")
	ErrTruncated = errors.New("TROPUSR.DAT is truncated")
	ErrCorrupt   = errors.New("TROPUSR.DAT has an invalid table")
)

// Trophy is one entry of the unlock table.
type Trophy struct {
	ID       int
	Unlocked bool
	Time     time.Time // zero when the file has no usable time
}

// ParseTROPUSR reads the unlock table (type 6). Other tables are skipped.
func ParseTROPUSR(data []byte) ([]Trophy, error) {
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("TROPUSR.DAT larger than %d bytes", MaxFileSize)
	}
	if len(data) < headerSize {
		return nil, ErrTruncated
	}
	be := binary.BigEndian
	if be.Uint32(data[0:4]) != tropusrMagic {
		return nil, ErrBadMagic
	}
	tables := be.Uint32(data[8:12])
	if tables == 0 || tables > maxTables {
		return nil, ErrCorrupt
	}
	if uint64(len(data)) < headerSize+uint64(tables)*tableHeaderSize {
		return nil, ErrTruncated
	}
	var out []Trophy
	found := false
	for i := uint32(0); i < tables; i++ {
		h := data[headerSize+i*tableHeaderSize:]
		if be.Uint32(h[0:4]) != 6 {
			continue
		}
		if found {
			return nil, ErrCorrupt // two unlock tables
		}
		found = true
		count := be.Uint32(h[12:16])
		offset := be.Uint64(h[16:24])
		if count > maxEntries {
			return nil, ErrCorrupt
		}
		end := offset + uint64(count)*entry6Size
		if offset < headerSize || end < offset || end > uint64(len(data)) {
			return nil, ErrTruncated
		}
		seen := map[int]bool{}
		for j := uint64(0); j < uint64(count); j++ {
			e := data[offset+j*entry6Size : offset+(j+1)*entry6Size]
			if be.Uint32(e[0:4]) != 6 {
				return nil, ErrCorrupt
			}
			id := be.Uint32(e[16:20])
			if id > 255 || seen[int(id)] {
				return nil, ErrCorrupt
			}
			seen[int(id)] = true
			t := Trophy{ID: int(id), Unlocked: be.Uint32(e[20:24]) == 1}
			if t.Unlocked {
				t.Time = tickToTime(be.Uint64(e[40:48]))
			}
			out = append(out, t)
		}
	}
	if !found {
		return nil, ErrCorrupt
	}
	return out, nil
}

// tickToTime converts a CellRtcTick; out-of-range values give the zero time.
func tickToTime(tick uint64) time.Time {
	const maxTick = rtcEpochOffset + 4102444800*1000000 // 2100-01-01
	if tick <= rtcEpochOffset || tick > maxTick {
		return time.Time{}
	}
	return time.UnixMicro(int64(tick - rtcEpochOffset)).UTC()
}
