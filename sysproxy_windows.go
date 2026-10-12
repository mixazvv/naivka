//go:build windows

package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
)

// proxyBackup — как было до нас. Хранится в файле, пока прокси включён.
type proxyBackup struct {
	Enable      uint32 `json:"enable"`
	Server      string `json:"server"`
	HasServer   bool   `json:"hasServer"`
	Override    string `json:"override"`
	HasOverride bool   `json:"hasOverride"`
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
		data, _ := json.Marshal(b)
		if err := os.WriteFile(backupPath(dir), data, 0o600); err != nil {
			return err
		}
	}
	bk := loadBackup(dir)

	if err := setString(k, "ProxyServer", fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
		return err
	}
	_ = setString(k, "ProxyOverride", bypassList())
	if err := setDWORD(k, "ProxyEnable", 1); err != nil {
		return err
	}
	notifyProxyChanged()

	// Страховка: если компьютер выключат или Naivka упадёт, при следующем входе
	// Windows сама вернёт прокси как было (RunOnce выполняется один раз и удаляется).
	if rk, err := createKey(runOnceKey); err == nil {
		_ = setString(rk, runOnceName, restoreCommand(bk))
		syscall.RegCloseKey(rk)
	}
	return nil
}

func loadBackup(dir string) proxyBackup {
	var b proxyBackup
	data, err := os.ReadFile(backupPath(dir))
	if err == nil {
		_ = json.Unmarshal(data, &b)
	}
	return b
}

func restoreCommand(b proxyBackup) string {
	key := `"HKCU\` + inetKey + `"`
	cmd := fmt.Sprintf(`cmd.exe /c reg add %s /v ProxyEnable /t REG_DWORD /d %d /f`, key, b.Enable)
	if b.HasServer && b.Server != "" && !strings.ContainsAny(b.Server, "\"&%^|<>") {
		cmd += fmt.Sprintf(` & reg add %s /v ProxyServer /t REG_SZ /d "%s" /f`, key, b.Server)
	}
	return cmd
}

func restoreSystemProxy(dir string) {
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
	notifyProxyChanged()
	_ = os.Remove(backupPath(dir))
}
