//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

var (
	shell32              = syscall.NewLazyDLL("shell32.dll")
	procIsUserAnAdmin    = shell32.NewProc("IsUserAnAdmin")
	procShellExecuteW    = shell32.NewProc("ShellExecuteW")
	user32               = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW      = user32.NewProc("MessageBoxW")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW = kernel32.NewProc("CreateJobObjectW")
	procSetInfoJobObject = kernel32.NewProc("SetInformationJobObject")
	procAssignProcToJob  = kernel32.NewProc("AssignProcessToJobObject")
)

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func isAdmin() bool {
	r, _, _ := procIsUserAnAdmin.Call()
	return r != 0
}

func relaunchElevated(replace, connect bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := "--no-elevate"
	if replace {
		args += " --replace"
	}
	if connect {
		args += " --connect"
	}
	r, _, _ := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(u16("runas"))),
		uintptr(unsafe.Pointer(u16(exe))),
		uintptr(unsafe.Pointer(u16(args))),
		uintptr(unsafe.Pointer(u16(filepath.Dir(exe)))),
		1) // SW_SHOWNORMAL
	if r <= 32 {
		return fmt.Errorf("запуск с правами администратора отменён (код %d)", r)
	}
	return nil
}

func messageBox(title, text string) {
	procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(u16(text))),
		uintptr(unsafe.Pointer(u16(title))),
		0x10) // MB_ICONERROR
}

// hideWindow — чтобы у sing-box не появлялось консольное окно.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

// ---------- Job Object: если Naivka упадёт, sing-box завершится вместе с ней ----------

type ioCounters struct {
	ReadOperationCount, WriteOperationCount, OtherOperationCount uint64
	ReadTransferCount, WriteTransferCount, OtherTransferCount    uint64
}

type jobBasicLimit struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobExtLimit struct {
	Basic                 jobBasicLimit
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

var (
	jobOnce   sync.Once
	jobHandle uintptr
)

func attachToJob(p *os.Process) {
	jobOnce.Do(func() {
		h, _, _ := procCreateJobObjectW.Call(0, 0)
		if h == 0 {
			return
		}
		var info jobExtLimit
		info.Basic.LimitFlags = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		r, _, _ := procSetInfoJobObject.Call(h, 9 /* JobObjectExtendedLimitInformation */, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
		if r == 0 {
			syscall.CloseHandle(syscall.Handle(h))
			return
		}
		jobHandle = h
	})
	if jobHandle == 0 || p == nil {
		return
	}
	ph, err := syscall.OpenProcess(0x0100|0x0001, false, uint32(p.Pid)) // PROCESS_SET_QUOTA | PROCESS_TERMINATE
	if err != nil {
		return
	}
	defer syscall.CloseHandle(ph)
	procAssignProcToJob.Call(jobHandle, uintptr(ph))
}

// ---------- окно интерфейса ----------

func findBrowser() string {
	roots := []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LocalAppData")}
	for _, rel := range []string{`Microsoft\Edge\Application\msedge.exe`, `Google\Chrome\Application\chrome.exe`} {
		for _, root := range roots {
			if root == "" {
				continue
			}
			p := filepath.Join(root, rel)
			if fileExists(p) {
				return p
			}
		}
	}
	return ""
}

// browserProfile — отдельный профиль Edge для окна Naivka (у запущенной с правами — свой).
func browserProfile(dir string) string {
	if isAdmin() {
		return filepath.Join(dir, "window-admin")
	}
	return filepath.Join(dir, "window")
}

// openWindow открывает интерфейс отдельным окном Edge (режим приложения, без вкладок
// и адресной строки). Возвращает процесс окна, если его можно отслеживать.
func openWindow(url, profile string) *exec.Cmd {
	if b := findBrowser(); b != "" {
		cmd := exec.Command(b,
			"--app="+url,
			"--user-data-dir="+profile,
			"--window-size=1180,820",
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-features=Translate",
			"--lang=ru",
		)
		if err := cmd.Start(); err == nil {
			return cmd
		}
	}
	_ = exec.Command(sysExe("rundll32"), "url.dll,FileProtocolHandler", url).Start()
	return nil
}

var (
	ntdll             = syscall.NewLazyDLL("ntdll.dll")
	procRtlGetVersion = ntdll.NewProc("RtlGetVersion")
)

type osVersionInfo struct {
	Size, Major, Minor, Build, Platform uint32
	CSD                                 [128]uint16
}

func windowsVersion() (major, minor, build uint32) {
	v := osVersionInfo{Size: uint32(unsafe.Sizeof(osVersionInfo{}))}
	procRtlGetVersion.Call(uintptr(unsafe.Pointer(&v)))
	return v.Major, v.Minor, v.Build
}

// legacyWindows — Windows 7/8/8.1: для них нужна отдельная сборка sing-box.
func legacyWindows() bool {
	major, _, _ := windowsVersion()
	return major < 10
}

// protectedBase — папка, куда может писать только администратор.
func protectedBase() string {
	if !isAdmin() {
		return ""
	}
	pf := programFilesDir()
	if pf == "" {
		return ""
	}
	return filepath.Join(pf, "Naivka")
}

func openURL(u string) {
	_ = exec.Command(sysExe("rundll32"), "url.dll,FileProtocolHandler", u).Start()
}
