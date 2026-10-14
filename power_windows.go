//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	powrprof         = syscall.NewLazyDLL("powrprof.dll")
	procPowerRegSusp = powrprof.NewProc("PowerRegisterSuspendResumeNotification")
)

// DEVICE_NOTIFY_SUBSCRIBE_PARAMETERS. Лежит в глобальной переменной: Windows хранит указатель на неё.
type deviceNotifyParams struct {
	Callback uintptr
	Context  uintptr
}

var (
	powerParams deviceNotifyParams
	powerHandle uintptr
)

// watchPower подписывается на сон и пробуждение (Windows 8 и новее; на Windows 7 функции нет).
func (a *App) watchPower() {
	if procPowerRegSusp.Find() != nil {
		return
	}
	powerParams.Callback = syscall.NewCallback(func(ctx, typ, setting uintptr) uintptr {
		switch typ {
		case 4: // PBT_APMSUSPEND
			go a.onSuspend()
		case 7, 18: // PBT_APMRESUMESUSPEND, PBT_APMRESUMEAUTOMATIC
			go a.onResume()
		}
		return 0
	})
	procPowerRegSusp.Call(2, uintptr(unsafe.Pointer(&powerParams)), uintptr(unsafe.Pointer(&powerHandle))) // DEVICE_NOTIFY_CALLBACK
}
