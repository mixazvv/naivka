package main

import (
	"encoding/binary"
	"net"
)

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

// Stable route attributes for operation freshness; lifetime/age counters are
// excluded because the OS updates them without a routing policy change.
func repairRouteSignature(data []byte) []string {
	if len(data) < 8 {
		return nil
	}
	n := binary.LittleEndian.Uint32(data)
	if n > 16384 || len(data) < 8+int(n)*104 {
		return nil
	}
	result := make([]string, 0, n)
	for i := 0; i < int(n); i++ {
		row := data[8+i*104 : 8+(i+1)*104]
		b := append([]byte{}, row[8:41]...)
		b = append(b, row[48:76]...)
		b = append(b, row[84:88]...)
		result = append(result, string(b))
	}
	return result
}

// Counts public more-specific routes on other interfaces. These can be intended
// exclusions (including the VPN endpoint); presence calls for review, not a leak
// verdict. Default physical routes are expected beneath a TUN /0 or two /1s.
func repairRouteAudit(data []byte, index uint32) (ipv4, ipv6, bypass string) {
	v4, v6, known := repairRouteCoverage(data, index)
	if !known {
		return "UNKNOWN", "UNKNOWN", "UNKNOWN"
	}
	ipv4, ipv6, bypass = "MISSING", "MISSING", "NONE_OBSERVED"
	if v4 {
		ipv4 = "OK"
	}
	if v6 {
		ipv6 = "OK"
	}
	own4, own6 := byte(255), byte(255)
	n := int(binary.LittleEndian.Uint32(data))
	for i := 0; i < n; i++ {
		row := data[8+i*104 : 8+(i+1)*104]
		if binary.LittleEndian.Uint32(row[8:12]) != index {
			continue
		}
		family := binary.LittleEndian.Uint16(row[12:14])
		if family == 2 && row[40] < own4 {
			own4 = row[40]
		}
		if family == 23 && row[40] < own6 {
			own6 = row[40]
		}
	}
	for i := 0; i < n; i++ {
		row := data[8+i*104 : 8+(i+1)*104]
		if binary.LittleEndian.Uint32(row[8:12]) == index {
			continue
		}
		family := binary.LittleEndian.Uint16(row[12:14])
		prefix := row[40]
		var ip net.IP
		switch family {
		case 2:
			if !v4 || prefix <= own4 || prefix > 32 {
				continue
			}
			ip = net.IP(row[16:20])
		case 23:
			if !v6 || prefix <= own6 || prefix > 128 {
				continue
			}
			ip = net.IP(row[20:36])
		default:
			continue
		}
		if ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
			bypass = "REVIEW_EXCLUSIONS"
		}
	}
	return
}
