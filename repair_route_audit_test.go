package main

import (
	"encoding/binary"
	"testing"
)

func TestRepairRouteAuditAndFreshness(t *testing.T) {
	data := make([]byte, 8+4*104)
	binary.LittleEndian.PutUint32(data, 4)
	row := func(n int, idx uint32, family uint16, prefix byte) []byte {
		r := data[8+n*104 : 8+(n+1)*104]
		binary.LittleEndian.PutUint32(r[8:], idx)
		binary.LittleEndian.PutUint16(r[12:], family)
		r[40] = prefix
		return r
	}
	row(0, 7, 2, 1)
	row(1, 7, 2, 1)[16] = 128
	row(2, 7, 23, 0)
	row(3, 2, 2, 0)
	v4, v6, extra := repairRouteAudit(data, 7)
	if v4 != "OK" || v6 != "OK" || extra != "NONE_OBSERVED" {
		t.Fatal(v4, v6, extra)
	}
	r := row(3, 2, 2, 32)
	r[16] = 8
	r[17] = 8
	r[18] = 8
	r[19] = 8
	_, _, extra = repairRouteAudit(data, 7)
	if extra != "REVIEW_EXCLUSIONS" {
		t.Fatal(extra)
	}
	a := repairRouteSignature(data)
	r[76]++
	r[80]++ // lifetimes
	b := repairRouteSignature(data)
	if a[3] != b[3] {
		t.Fatal("volatile lifetime invalidates transaction")
	}
	r[41]++
	r[42]++
	if repairRouteSignature(data)[3] != b[3] {
		t.Fatal("padding invalidates transaction")
	}
	r[84]++
	if repairRouteSignature(data)[3] == b[3] {
		t.Fatal("metric change not detected")
	}
	r[16] = 192
	r[17] = 168
	_, _, extra = repairRouteAudit(data, 7)
	if extra != "NONE_OBSERVED" {
		t.Fatal("private route called public exclusion")
	}
	if _, _, extra = repairRouteAudit(data[:12], 7); extra != "UNKNOWN" {
		t.Fatal("truncation")
	}
}
