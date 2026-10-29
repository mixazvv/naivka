//go:build windows

package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

const (
	inetKey     = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	runOnceKey  = `Software\Microsoft\Windows\CurrentVersion\RunOnce`
	runOnceName = "NaivkaRestoreProxy"

	regSZ    = 1
	regDWORD = 4
)

var (
	advapi32            = syscall.NewLazyDLL("advapi32.dll")
	procRegSetValueExW  = advapi32.NewProc("RegSetValueExW")
	procRegDeleteValueW = advapi32.NewProc("RegDeleteValueW")
	procRegCreateKeyExW = advapi32.NewProc("RegCreateKeyExW")
	wininet             = syscall.NewLazyDLL("wininet.dll")
	procInternetSetOpt  = wininet.NewProc("InternetSetOptionW")
	procInternetQryOpt  = wininet.NewProc("InternetQueryOptionW")
)

// Настройки подключения через WinINet (INTERNET_OPTION_PER_CONNECTION_OPTION): так их меняют
// «Параметры» Windows. Значения в реестре пишем тоже — их читают программы, которые смотрят только туда.
const (
	optPerConn      = 75 // INTERNET_OPTION_PER_CONNECTION_OPTION
	connFlags       = 1  // INTERNET_PER_CONN_FLAGS
	connProxyServer = 2
	connBypass      = 3
	connAutoURL     = 4
	typeDirect      = 1 // PROXY_TYPE_DIRECT
	typeProxy       = 2
	typeAutoURL     = 4
	typeAutoDetect  = 8
)

// Value — объединение DWORD / указатель / FILETIME, 8 байт: uint64 даёт верную раскладку и на 64-,
// и на 32-битной Windows (там структура 12 байт, выравнивание по 4).
type perConnOption struct {
	Option uint32
	Value  uint64 // DWORD или указатель на строку UTF-16
}

type perConnList struct {
	Size       uint32
	Connection *uint16 // nil — подключение по локальной сети (как и ключ реестра)
	Count      uint32
	Error      uint32
	Options    *perConnOption
}

// setPerConn применяет настройки; strs — строки, на которые указывают значения (держим их живыми до вызова).
func setPerConn(opts []perConnOption, strs ...[]uint16) bool {
	l := perConnList{Count: uint32(len(opts)), Options: &opts[0]}
	l.Size = uint32(unsafe.Sizeof(l))
	r, _, _ := procInternetSetOpt.Call(0, optPerConn, uintptr(unsafe.Pointer(&l)), uintptr(l.Size))
	runtime.KeepAlive(opts)
	runtime.KeepAlive(strs)
	return r != 0
}

// strOpt — строковый параметр подключения; строку s нужно передать в setPerConn.
func strOpt(option uint32, v string) (perConnOption, []uint16) {
	s, _ := syscall.UTF16FromString(v)
	return perConnOption{Option: option, Value: uint64(uintptr(unsafe.Pointer(&s[0])))}, s
}

// queryConnFlags — текущие флаги подключения (PROXY_TYPE_*); ok=false — не удалось узнать.
func queryConnFlags() (uint32, bool) {
	opts := []perConnOption{{Option: connFlags}}
	l := perConnList{Count: 1, Options: &opts[0]}
	l.Size = uint32(unsafe.Sizeof(l))
	n := l.Size
	r, _, _ := procInternetQryOpt.Call(0, optPerConn, uintptr(unsafe.Pointer(&l)), uintptr(unsafe.Pointer(&n)))
	return uint32(opts[0].Value), r != 0
}

// proxyBackup — как было до нас. Хранится в файле, пока прокси включён.
type proxyBackup struct {
	Enable      uint32 `json:"enable"`
	Server      string `json:"server"`
	HasServer   bool   `json:"hasServer"`
	Override    string `json:"override"`
	HasOverride bool   `json:"hasOverride"`
	Auto        string `json:"auto,omitempty"` // AutoConfigURL (свой PAC-скрипт пользователя)
	HasAuto     bool   `json:"hasAuto,omitempty"`
	Flags       uint32 `json:"flags,omitempty"` // флаги WinINet (0 — не знаем, файл прежних версий)
}

const autoKey = "AutoConfigURL"

// ourPAC — адрес похож на скрипт Naivka (в том числе от прошлого запуска, с другим портом).
func ourPAC(u string) bool {
	return strings.HasPrefix(u, "http://127.0.0.1:") && strings.Contains(u, "/naivka-")
}

func backupPath(dir string) string { return filepath.Join(dir, "proxy-backup.json") }

func openKey(path string, access uint32) (syscall.Handle, error) {
	var h syscall.Handle
	err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, u16(path), 0, access, &h)
	return h, err
}

func createKey(path string) (syscall.Handle, error) {
	var h syscall.Handle
	r, _, _ := procRegCreateKeyExW.Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(u16(path))),
		0, 0, 0, uintptr(syscall.KEY_READ|syscall.KEY_WRITE), 0, uintptr(unsafe.Pointer(&h)), 0)
	if r != 0 {
		return 0, syscall.Errno(r)
	}
	return h, nil
}

func readDWORD(k syscall.Handle, name string) (uint32, bool) {
	var typ uint32
	var buf [4]byte
	n := uint32(4)
	if err := syscall.RegQueryValueEx(k, u16(name), nil, &typ, &buf[0], &n); err != nil || typ != regDWORD {
		return 0, false
	}
	return binary.LittleEndian.Uint32(buf[:]), true
}

func readString(k syscall.Handle, name string) (string, bool) {
	var typ, n uint32
	if err := syscall.RegQueryValueEx(k, u16(name), nil, &typ, nil, &n); err != nil || n == 0 {
		return "", err == nil
	}
	buf := make([]uint16, n/2+1)
	if err := syscall.RegQueryValueEx(k, u16(name), nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &n); err != nil {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}

func setString(k syscall.Handle, name, v string) error {
	data, _ := syscall.UTF16FromString(v)
	r, _, _ := procRegSetValueExW.Call(uintptr(k), uintptr(unsafe.Pointer(u16(name))), 0, regSZ,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func setDWORD(k syscall.Handle, name string, v uint32) error {
	r, _, _ := procRegSetValueExW.Call(uintptr(k), uintptr(unsafe.Pointer(u16(name))), 0, regDWORD,
		uintptr(unsafe.Pointer(&v)), 4)
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func deleteValue(k syscall.Handle, name string) {
	procRegDeleteValueW.Call(uintptr(k), uintptr(unsafe.Pointer(u16(name))))
}

func notifyProxyChanged() {
	procInternetSetOpt.Call(0, 39, 0, 0) // INTERNET_OPTION_SETTINGS_CHANGED
	procInternetSetOpt.Call(0, 37, 0, 0) // INTERNET_OPTION_REFRESH
}

func bypassList() string {
	parts := []string{"localhost", "127.*", "10.*", "192.168.*"}
	for i := 16; i <= 31; i++ {
		parts = append(parts, fmt.Sprintf("172.%d.*", i))
	}
	return strings.Join(parts, ";") + ";<local>"
}

func setSystemProxy(dir string, port int) error {
	k, err := openKey(inetKey, syscall.KEY_READ|syscall.KEY_WRITE)
	if err != nil {
		return err
	}
	defer syscall.RegCloseKey(k)

	// Запоминаем исходное состояние только один раз — чтобы не сохранить «своё» как «чужое».
	if !fileExists(backupPath(dir)) {
		var b proxyBackup
		b.Enable, _ = readDWORD(k, "ProxyEnable")
		b.Server, b.HasServer = readString(k, "ProxyServer")
		b.Override, b.HasOverride = readString(k, "ProxyOverride")
		b.Auto, b.HasAuto = readString(k, autoKey)
		if ourPAC(b.Auto) { // остался от сбоя прежнего запуска — это не настройка пользователя
			b.Auto, b.HasAuto = "", false
		}
		if f, ok := queryConnFlags(); ok {
			b.Flags = f | typeDirect
		}
		data, _ := json.Marshal(b)
		if err := os.WriteFile(backupPath(dir), data, 0o600); err != nil {
			return err
		}
	}

	// Себя в автозапуск (RunOnce) не прописываем: Защитник Windows принимал это за троян
	// (Trojan:Win32/Bearfoos.A!ml) и удалял Naivka.exe. Прокси вернётся при следующем запуске Naivka.
	if !usePAC() {
		return setDirectProxy(k, port)
	}
	// Прокси — PAC-скриптом, который раздаёт Naivka (pac.go): выключили компьютер или Naivka упала —
	// скрипта нет, и всё идёт напрямую, а не в никуда. Прямой адрес прокси выключаем, иначе Windows
	// откатилась бы на него, не получив скрипт.
	u, err := pacURL(port)
	if err != nil {
		return err
	}
	if err := setString(k, autoKey, u); err != nil {
		return err
	}
	if err := setDWORD(k, "ProxyEnable", 0); err != nil {
		return err
	}
	ao, as := strOpt(connAutoURL, u)
	_ = setPerConn([]perConnOption{{Option: connFlags, Value: typeDirect | typeAutoURL}, ao}, as)
	notifyProxyChanged()
	return nil
}

// setDirectProxy — прямой адрес 127.0.0.1:port (как до 2.16.2); наш скрипт, если остался, убираем:
// Windows сперва смотрит на скрипт.
func setDirectProxy(k syscall.Handle, port int) error {
	if a, _ := readString(k, autoKey); ourPAC(a) {
		deleteValue(k, autoKey)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if err := setString(k, "ProxyServer", addr); err != nil {
		return err
	}
	_ = setString(k, "ProxyOverride", bypassList())
	if err := setDWORD(k, "ProxyEnable", 1); err != nil {
		return err
	}
	so, ss := strOpt(connProxyServer, addr)
	bo, bs := strOpt(connBypass, bypassList())
	ao, as := strOpt(connAutoURL, "")
	_ = setPerConn([]perConnOption{{Option: connFlags, Value: typeDirect | typeProxy}, so, bo, ao}, ss, bs, as)
	notifyProxyChanged()
	return nil
}

// systemProxyOurs — указывает ли системный прокси Windows на Naivka; current — что там сейчас.
func systemProxyOurs(port int) (ours bool, current string) {
	k, err := openKey(inetKey, syscall.KEY_READ)
	if err != nil {
		return true, "" // не смогли прочитать — не поднимаем тревогу
	}
	defer syscall.RegCloseKey(k)
	auto, _ := readString(k, autoKey)
	if !usePAC() {
		en, _ := readDWORD(k, "ProxyEnable")
		srv, _ := readString(k, "ProxyServer")
		if auto != "" && !ourPAC(auto) {
			return false, auto // чужой скрипт главнее прямого адреса
		}
		if en == 1 && srv == fmt.Sprintf("127.0.0.1:%d", port) {
			return true, srv
		}
		if en != 1 {
			return false, ""
		}
		return false, srv
	}
	pacSrv.mu.Lock()
	mine := pacSrv.url
	pacSrv.mu.Unlock()
	if mine != "" && auto == mine {
		return true, auto
	}
	if auto != "" {
		return false, auto
	}
	en, _ := readDWORD(k, "ProxyEnable")
	srv, _ := readString(k, "ProxyServer")
	if en != 1 {
		return false, ""
	}
	return false, srv
}

func loadBackup(dir string) proxyBackup {
	var b proxyBackup
	data, err := os.ReadFile(backupPath(dir))
	if err == nil {
		_ = json.Unmarshal(data, &b)
	}
	return b
}

func restoreSystemProxy(dir string) {
	// Запись прежних версий (до 2.16.1) — убираем, если осталась.
	if rk, err := openKey(runOnceKey, syscall.KEY_READ|syscall.KEY_WRITE); err == nil {
		deleteValue(rk, runOnceName)
		syscall.RegCloseKey(rk)
	}
	if !fileExists(backupPath(dir)) {
		return
	}
	b := loadBackup(dir)
	k, err := openKey(inetKey, syscall.KEY_READ|syscall.KEY_WRITE)
	if err != nil {
		return
	}
	defer syscall.RegCloseKey(k)
	_ = setDWORD(k, "ProxyEnable", b.Enable)
	if b.HasServer {
		_ = setString(k, "ProxyServer", b.Server)
	} else {
		deleteValue(k, "ProxyServer")
	}
	if b.HasOverride {
		_ = setString(k, "ProxyOverride", b.Override)
	} else {
		deleteValue(k, "ProxyOverride")
	}
	// Скрипт: свой у пользователя — вернуть; наш (этого или прошлого запуска) — убрать.
	auto, _ := readString(k, autoKey)
	if b.HasAuto {
		_ = setString(k, autoKey, b.Auto)
	} else if ourPAC(auto) {
		deleteValue(k, autoKey)
		auto = ""
	}
	if b.HasAuto {
		auto = b.Auto
	}
	// Флаги WinINet — как были (с «Автоматически определять параметры»). Файл прежних версий их
	// не хранит — тогда берём нынешние и правим только прокси и скрипт.
	base := b.Flags
	if base == 0 {
		base, _ = queryConnFlags()
	}
	if base != 0 {
		flags := (base | typeDirect) &^ (typeProxy | typeAutoURL)
		if b.Enable == 1 {
			flags |= typeProxy
		}
		if auto != "" {
			flags |= typeAutoURL
		}
		opts := []perConnOption{{Option: connFlags, Value: uint64(flags)}}
		var strs [][]uint16
		add := func(option uint32, v string) {
			o, s := strOpt(option, v)
			opts = append(opts, o)
			strs = append(strs, s)
		}
		add(connAutoURL, auto)
		if b.HasServer {
			add(connProxyServer, b.Server)
		}
		if b.HasOverride {
			add(connBypass, b.Override)
		}
		_ = setPerConn(opts, strs...)
	}
	notifyProxyChanged()
	_ = os.Remove(backupPath(dir))
}
