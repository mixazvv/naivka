//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Защита от подмены библиотек: DLL ищутся только в System32, а не в папке программы.
// Иначе чужая программа могла бы положить рядом с Naivka.exe, скажем, dwmapi.dll или
// WebView2Loader.dll, и её код выполнился бы с правами Naivka — в том числе администратора.
// Загрузчик WebView2 при этом берётся из копии, встроенной в exe.
func init() {
	const loadLibrarySearchSystem32 = 0x00000800
	if p := kernel32.NewProc("SetDefaultDllDirectories"); p.Find() == nil {
		p.Call(loadLibrarySearchSystem32)
	}
}

var (
	ole32                    = syscall.NewLazyDLL("ole32.dll")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
	procSHGetKnownFolderPath = shell32.NewProc("SHGetKnownFolderPath")
)

// GUID известных папок Windows.
var (
	folderProgramFiles    = syscall.GUID{Data1: 0x905e63b6, Data2: 0xc1bf, Data3: 0x494e, Data4: [8]byte{0xb2, 0x9c, 0x65, 0xb7, 0x32, 0xd3, 0xd2, 0x1a}}
	folderProgramFilesX86 = syscall.GUID{Data1: 0x7c5a40ef, Data2: 0xa0fb, Data3: 0x4bfc, Data4: [8]byte{0x87, 0x4a, 0xc0, 0xf2, 0xe0, 0xb9, 0xfa, 0x8e}}
	folderWindows         = syscall.GUID{Data1: 0xf38bf404, Data2: 0x1d43, Data3: 0x42f2, Data4: [8]byte{0x93, 0x05, 0x67, 0xde, 0x0b, 0x28, 0xfc, 0x23}}
)

// knownFolder — путь к системной папке от самой Windows. Переменным окружения (%ProgramFiles%)
// верить нельзя: пользователь может задать свои, и они достаются и процессу с правами администратора.
func knownFolder(id *syscall.GUID) string {
	if procSHGetKnownFolderPath.Find() != nil {
		return ""
	}
	var p *uint16
	r, _, _ := procSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(id)), 0, 0, uintptr(unsafe.Pointer(&p)))
	if r != 0 || p == nil {
		return ""
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return syscall.UTF16ToString((*[1 << 15]uint16)(unsafe.Pointer(p))[:])
}

// sysExe — полный путь к системной программе (System32). По одному имени Windows ищет программу
// и в PATH пользователя, и в текущей папке (обычно папке с Naivka.exe) — подложенный там schtasks.exe
// или cmd.exe получил бы права администратора вместе с Naivka.
func sysExe(name string) string {
	if win := knownFolder(&folderWindows); win != "" {
		return filepath.Join(win, "System32", name+".exe")
	}
	return name
}

// exeProtected — лежит ли программа там, куда без прав администратора не записать
// (Program Files, Windows). Только такой exe можно запускать с правами без запроса UAC.
func exeProtected(exe string) bool {
	exe = strings.ToLower(filepath.Clean(exe))
	for _, id := range []*syscall.GUID{&folderProgramFiles, &folderProgramFilesX86, &folderWindows} {
		if d := knownFolder(id); d != "" && strings.HasPrefix(exe, strings.ToLower(filepath.Clean(d))+`\`) {
			return true
		}
	}
	return false
}

var restricted sync.Map

// restrictDir оставляет доступ к папке только системе, администраторам и текущему пользователю.
// Нужна для папки run в Program Files: там конфиг с паролями и списки открытых сайтов, а Program Files
// по умолчанию читают все учётные записи компьютера.
func restrictDir(dir string) {
	if protectedBase() == "" || !strings.HasPrefix(strings.ToLower(dir), strings.ToLower(protectedBase())) {
		return // в %APPDATA% доступ и так только у владельца
	}
	if _, done := restricted.LoadOrStore(dir, true); done {
		return
	}
	grants := []string{"*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F"}
	if sid := currentUserSID(); sid != "" {
		grants = append(grants, "*"+sid+":(OI)(CI)F")
	}
	// Права ставятся только самой папке, а файлы внутри сбрасываются на «наследовать от папки».
	// (2.3.6 применяла правило и к файлам с ключом /T — у уже лежавших файлов права пропадали
	// совсем, и ядро не могло записать config.json. Сброс ниже чинит и это.)
	run := func(args ...string) error {
		// Ограничение по времени: зависший icacls не должен навсегда задержать подключение.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, sysExe("icacls"), args...)
		hideWindow(cmd)
		return cmd.Run()
	}
	err := run(append(append([]string{dir, "/inheritance:r", "/grant:r"}, grants...), "/C", "/Q")...)
	if err2 := run(filepath.Join(dir, "*"), "/reset", "/T", "/C", "/Q"); err == nil {
		err = err2
	}
	if err != nil {
		restricted.Delete(dir)
	}
}

func currentUserSID() string {
	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return ""
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return ""
	}
	s, err := u.User.Sid.String()
	if err != nil {
		return ""
	}
	return s
}

// programFilesDir — Program Files от Windows; переменная окружения — только запасной вариант.
func programFilesDir() string {
	if d := knownFolder(&folderProgramFiles); d != "" {
		return d
	}
	return os.Getenv("ProgramFiles")
}
