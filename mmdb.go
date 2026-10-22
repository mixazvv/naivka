package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"net"
)

// Минимальное чтение базы MaxMind DB (формат .mmdb): только поиск адреса и разбор найденной
// записи. Нужна для страны сайта по его IP (база server-country, см. ipcountry.go) — без сторонних
// библиотек и без запросов наружу. Формат: https://maxmind.github.io/MaxMind-DB/

var mmdbMarker = []byte("\xAB\xCD\xEFMaxMind.com")

type mmdb struct {
	buf        []byte
	nodeCount  uint
	recordSize uint
	ipVersion  uint
	dataStart  uint
	ipv4Start  uint // узел, с которого начинаются IPv4-адреса (::/96) в базе IPv6
	budget     int  // сколько ещё значений можно разобрать за один разбор (защита от «бомб» из указателей)
}

// decodeBudget — больше значений одна запись базы стран не содержит; испорченный файл с указателями
// на одну и ту же большую карту иначе разворачивался бы до нехватки памяти.
const decodeBudget = 10000

func openMMDB(b []byte) (db *mmdb, err error) {
	// Испорченный файл не должен уронить Naivka: любая ошибка разбора — просто «база не открылась».
	defer func() {
		if recover() != nil {
			db, err = nil, errors.New("повреждена база MMDB")
		}
	}()
	i := bytes.LastIndex(b, mmdbMarker)
	if i < 0 {
		return nil, errors.New("это не база MMDB")
	}
	db = &mmdb{buf: b}
	meta, _, err := db.decode(uint(i+len(mmdbMarker)), uint(i+len(mmdbMarker)))
	if err != nil {
		return nil, err
	}
	m, ok := meta.(map[string]any)
	if !ok {
		return nil, errors.New("повреждены сведения о базе MMDB")
	}
	num := func(k string) uint { v, _ := m[k].(uint64); return uint(v) }
	db.nodeCount, db.recordSize, db.ipVersion = num("node_count"), num("record_size"), num("ip_version")
	if db.recordSize != 24 && db.recordSize != 28 && db.recordSize != 32 {
		return nil, errors.New("неизвестный размер записи MMDB")
	}
	// Узел занимает не меньше 6 байт: больше узлов, чем байт в файле, — испорченные сведения
	// (и защита от переполнения при умножении ниже).
	if db.nodeCount > uint(len(b)) {
		return nil, errors.New("повреждено дерево MMDB")
	}
	tree := db.nodeCount * db.recordSize / 4
	if db.nodeCount == 0 || tree+16 > uint(i) {
		return nil, errors.New("повреждено дерево MMDB")
	}
	db.dataStart = tree + 16
	if db.ipVersion == 6 {
		node := uint(0)
		for k := 0; k < 96 && node < db.nodeCount; k++ {
			node = db.record(node, 0)
		}
		db.ipv4Start = node
	}
	return db, nil
}

func (db *mmdb) record(node, bit uint) uint {
	b := db.buf
	switch db.recordSize {
	case 24:
		o := node*6 + bit*3
		return uint(b[o])<<16 | uint(b[o+1])<<8 | uint(b[o+2])
	case 28:
		o := node * 7
		if bit == 0 {
			return uint(b[o+3]&0xF0)<<20 | uint(b[o])<<16 | uint(b[o+1])<<8 | uint(b[o+2])
		}
		return uint(b[o+3]&0x0F)<<24 | uint(b[o+4])<<16 | uint(b[o+5])<<8 | uint(b[o+6])
	default:
		o := node*8 + bit*4
		return uint(binary.BigEndian.Uint32(b[o:]))
	}
}

// lookup — запись для адреса или nil, если адреса в базе нет.
func (db *mmdb) lookup(ip net.IP) (any, error) {
	var bits []byte
	node := uint(0)
	if v4 := ip.To4(); v4 != nil {
		bits = v4
		if db.ipVersion == 6 {
			node = db.ipv4Start
		}
	} else if db.ipVersion == 6 {
		bits = ip.To16()
	} else {
		return nil, nil
	}
	for i := 0; i < len(bits)*8 && node < db.nodeCount; i++ {
		bit := uint(bits[i/8]>>(7-uint(i%8))) & 1
		node = db.record(node, bit)
	}
	if node <= db.nodeCount {
		return nil, nil // == nodeCount — адреса нет; < nodeCount — дерево короче адреса (не бывает)
	}
	off := db.dataStart + (node - db.nodeCount - 16)
	if off >= uint(len(db.buf)) {
		return nil, errors.New("запись MMDB за пределами файла")
	}
	v, _, err := db.decode(off, db.dataStart)
	return v, err
}

// decode разбирает значение по смещению off; base — начало раздела данных (от него считаются указатели).
func (db *mmdb) decode(off, base uint) (any, uint, error) {
	db.budget = decodeBudget
	return db.decodeDepth(off, base, 0)
}

func (db *mmdb) decodeDepth(off, base uint, depth int) (any, uint, error) {
	b := db.buf
	bad := errors.New("повреждена запись MMDB")
	db.budget--
	if depth > 32 || off >= uint(len(b)) || db.budget < 0 {
		return nil, 0, bad
	}
	ctrl := b[off]
	off++
	typ := uint(ctrl >> 5)
	if typ == 1 { // указатель
		ss, vvv := uint(ctrl>>3)&3, uint(ctrl&7)
		if off+ss+1 > uint(len(b)) {
			return nil, 0, bad
		}
		var p uint
		switch ss {
		case 0:
			p = vvv<<8 | uint(b[off])
		case 1:
			p = (vvv<<16 | uint(b[off])<<8 | uint(b[off+1])) + 2048
		case 2:
			p = (vvv<<24 | uint(b[off])<<16 | uint(b[off+1])<<8 | uint(b[off+2])) + 526336
		default:
			p = uint(binary.BigEndian.Uint32(b[off:]))
		}
		v, _, err := db.decodeDepth(base+p, base, depth+1)
		return v, off + ss + 1, err
	}
	if typ == 0 { // расширенный тип
		if off >= uint(len(b)) {
			return nil, 0, bad
		}
		typ = 7 + uint(b[off])
		off++
	}
	size := uint(ctrl & 0x1F)
	if size >= 29 && off+size-28 > uint(len(b)) {
		return nil, 0, bad
	}
	switch size {
	case 29:
		size = 29 + uint(b[off])
		off++
	case 30:
		size = 285 + (uint(b[off])<<8 | uint(b[off+1]))
		off += 2
	case 31:
		size = 65821 + (uint(b[off])<<16 | uint(b[off+1])<<8 | uint(b[off+2]))
		off += 3
	}
	if off > uint(len(b)) {
		return nil, 0, bad
	}
	uintOf := func(n uint) (uint64, error) {
		if n > 8 || off+n > uint(len(b)) {
			return 0, bad
		}
		var v uint64
		for _, c := range b[off : off+n] {
			v = v<<8 | uint64(c)
		}
		return v, nil
	}
	switch typ {
	case 2, 4: // строка, байты
		if off+size > uint(len(b)) {
			return nil, 0, bad
		}
		return string(b[off : off+size]), off + size, nil
	case 3: // double
		if size != 8 || off+8 > uint(len(b)) {
			return nil, 0, bad
		}
		return math.Float64frombits(binary.BigEndian.Uint64(b[off:])), off + 8, nil
	case 15: // float
		if size != 4 || off+4 > uint(len(b)) {
			return nil, 0, bad
		}
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b[off:]))), off + 4, nil
	case 5, 6, 8, 9: // uint16, uint32, int32, uint64
		v, err := uintOf(size)
		return v, off + size, err
	case 10: // uint128 — в базах стран не бывает, пропускаем
		if off+size > uint(len(b)) {
			return nil, 0, bad
		}
		return nil, off + size, nil
	case 14: // boolean: значение в размере
		return size != 0, off, nil
	case 7: // map
		m := make(map[string]any, size)
		for i := uint(0); i < size; i++ {
			k, next, err := db.decodeDepth(off, base, depth+1)
			if err != nil {
				return nil, 0, err
			}
			ks, ok := k.(string)
			if !ok {
				return nil, 0, bad
			}
			v, next2, err := db.decodeDepth(next, base, depth+1)
			if err != nil {
				return nil, 0, err
			}
			m[ks] = v
			off = next2
		}
		return m, off, nil
	case 11: // array
		n := size
		if n > 1024 {
			n = 1024
		}
		a := make([]any, 0, n)
		for i := uint(0); i < size; i++ {
			v, next, err := db.decodeDepth(off, base, depth+1)
			if err != nil {
				return nil, 0, err
			}
			a = append(a, v)
			off = next
		}
		return a, off, nil
	}
	return nil, 0, bad
}
