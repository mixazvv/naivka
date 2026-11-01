//go:build windows

package main

import (
	"encoding/binary"
	"testing"
)

// Exercise the real read-only IP Helper call and native ROW2 layout on Windows.
// No adapter, route, firewall, proxy or VPN configuration is modified.
func TestRepairWindowsRouteSnapshot(t *testing.T) {
	data, err := readRepairRoutes()
	if err != nil {
		t.Fatal("IP Helper snapshot unavailable", err)
	}
	if len(data) < 8 {
		t.Fatal("missing native table header")
	}
	count := int(binary.LittleEndian.Uint32(data))
	if count == 0 || count > 16384 || len(data) != 8+count*104 {
		t.Fatal("unexpected native route table size")
	}
	for i := 0; i < count; i++ {
		row := data[8+i*104 : 8+(i+1)*104]
		family := binary.LittleEndian.Uint16(row[12:14])
		prefix := row[40]
		if family != 2 && family != 23 {
			t.Fatalf("native ROW2 family/layout mismatch at row %d", i)
		}
		if (family == 2 && prefix > 32) || (family == 23 && prefix > 128) {
			t.Fatalf("native prefix/layout mismatch at row %d", i)
		}
	}
	if _, _, known := repairRouteCoverage(data, ^uint32(0)); !known {
		t.Fatal("native table rejected by coverage parser")
	}
}
