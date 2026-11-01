package main

import "encoding/binary"

// MIB_IPFORWARD_TABLE2 has an 8-byte table header/alignment and 104-byte ROW2
// entries on supported Windows architectures. Only route coverage is inspected;
// it does not prove absence of more-specific bypasses or IPv6/DNS leaks.
func repairRouteCoverage(data []byte, index uint32) (ipv4, ipv6, known bool) {
	if len(data) < 8 {
		return
	}
	rawCount := binary.LittleEndian.Uint32(data)
	if rawCount > 16384 {
		return
	}
	count := int(rawCount)
	if len(data) < 8+count*104 {
		return
	}
	var v4low, v4high, v6low, v6high bool
	for i := 0; i < count; i++ {
		row := data[8+i*104 : 8+(i+1)*104]
		if binary.LittleEndian.Uint32(row[8:12]) != index {
			continue
		}
		family := binary.LittleEndian.Uint16(row[12:14])
		prefix := row[40]
		if family == 2 {
			address := binary.BigEndian.Uint32(row[16:20])
			if prefix == 0 && address == 0 {
				v4low = true
				v4high = true
			}
			if prefix == 1 {
				if address == 0 {
					v4low = true
				}
				if address == 0x80000000 {
					v4high = true
				}
			}
		}
		if family == 23 {
			zero := true
			for _, b := range row[21:36] {
				if b != 0 {
					zero = false
				}
			}
			if zero {
				if prefix == 0 && row[20] == 0 {
					v6low = true
					v6high = true
				}
				if prefix == 1 {
					if row[20] == 0 {
						v6low = true
					}
					if row[20] == 128 {
						v6high = true
					}
				}
			}
		}
	}
	return v4low && v4high, v6low && v6high, true
}
