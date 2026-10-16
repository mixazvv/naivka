//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// Автозапуск:
//   * системный прокси — запись в HKCU\...\Run, права администратора не нужны;
//   * TUN — задача Планировщика «при входе» с наивысшими правами: стартует без запроса UAC.
//     Только если Naivka установлена в Program Files (см. setAutostart).

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const taskName = "Naivka"

func setAutostart(enable, elevated bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Запуск с правами без запроса UAC — только для программы в Program Files. Если Naivka лежит
	// на рабочем столе или в «Загрузках», любой процесс пользователя мог бы подменить exe и при
	// следующем входе получить права администратора. Тогда автозапуск обычный, а права Naivka
	// попросит сама, через окно Windows.
	if elevated && !exeProtected(exe) {
		elevated = false
	}
	if k, err := createKey(runKey); err == nil {
		if enable && !elevated {
			_ = setString(k, appName, fmt.Sprintf(`"%s" --autostart`, exe))
		} else {
			deleteValue(k, appName)
		}
		syscall.RegCloseKey(k)
	}
	q := exec.Command("schtasks", "/Query", "/TN", taskName)
	hideWindow(q)
	taskExists := q.Run() == nil
	switch {
	case enable && elevated:
		xmlPath := filepath.Join(os.TempDir(), "naivka-task.xml")
		if err := os.WriteFile(xmlPath, utf16File(taskXML(exe)), 0o600); err != nil {
			return err
		}
		defer os.Remove(xmlPath)
		return runAdmin("schtasks", fmt.Sprintf(`/Create /F /TN %s /XML "%s"`, taskName, xmlPath))
	case taskExists:
		return runAdmin("schtasks", fmt.Sprintf(`/Delete /F /TN %s`, taskName))
	}
	return nil
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func taskXML(exe string) string {
	user := os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME")
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>Naivka: запуск при входе в систему</Description></RegistrationInfo>
  <Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + xmlEscape(user) + `</UserId><Delay>PT5S</Delay></LogonTrigger></Triggers>
  <Principals><Principal id="Author"><UserId>` + xmlEscape(user) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author"><Exec><Command>` + xmlEscape(exe) + `</Command><Arguments>--autostart</Arguments></Exec></Actions>
</Task>`
}

func utf16File(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2+len(u)*2)
	b[0], b[1] = 0xFF, 0xFE
	for i, c := range u {
		b[2+i*2], b[3+i*2] = byte(c), byte(c>>8)
	}
	return b
}

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     syscall.Handle
}

var procShellExecuteExW = shell32.NewProc("ShellExecuteExW")

// runAdmin запускает команду с правами администратора (если их нет — через UAC) и ждёт её.
func runAdmin(file, args string) error {
	if isAdmin() {
		cmd := exec.Command(file)
		hideWindow(cmd)
		cmd.SysProcAttr.CmdLine = file + " " + args
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %s", file, strings.TrimSpace(string(out)))
		}
		return nil
	}
	info := shellExecuteInfo{fMask: 0x40 | 0x100, lpVerb: u16("runas"), lpFile: u16(file), lpParameters: u16(args), nShow: 0}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return fmt.Errorf("нужны права администратора: %v", err)
	}
	defer syscall.CloseHandle(info.hProcess)
	if _, err := syscall.WaitForSingleObject(info.hProcess, 60000); err != nil {
		return err
	}
	var code uint32
	if err := syscall.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%s завершился с кодом %d", file, code)
	}
	return nil
}

// allowLANPort — разрешить входящие подключения к порту раздачи (один запрос UAC). Правило узкое:
// только частные сети, только устройства из той же подсети и только для самого ядра sing-box —
// другая программа на этом порту через него снаружи доступна не станет.
func allowLANPort(port int, program string) error {
	rule := "Naivka LAN"
	if strings.ContainsAny(program, `"&|<>^%`) {
		return fmt.Errorf("недопустимый путь к ядру: %s", program)
	}
	return runAdmin("cmd", fmt.Sprintf(`/c netsh advfirewall firewall delete rule name="%s" >nul 2>&1 & netsh advfirewall firewall add rule name="%s" dir=in action=allow protocol=TCP localport=%d profile=private remoteip=localsubnet program="%s"`, rule, rule, port, program))
}

// fixUnsafeAutostart убирает задачу с наивысшими правами, созданную прежними версиями для exe
// вне Program Files, и заменяет её обычным автозапуском. Вызывается при запуске с правами.
func fixUnsafeAutostart(s *Settings) {
	exe, err := os.Executable()
	if err != nil || !s.AutoStart || !s.needsAdmin() || exeProtected(exe) || !isAdmin() {
		return
	}
	q := exec.Command("schtasks", "/Query", "/TN", taskName)
	hideWindow(q)
	if q.Run() == nil {
		_ = setAutostart(true, true)
	}
}
