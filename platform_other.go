//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// Заглушки для других ОС: Naivka рассчитана на Windows, но собирается и
// запускается на Linux/macOS — это удобно для отладки генератора конфигов.

func isAdmin() bool { return os.Geteuid() == 0 }
func relaunchElevated(bool, bool) error {
	return fmt.Errorf("перезапуск с правами поддерживается только в Windows")
}
func messageBox(title, text string)    { fmt.Fprintln(os.Stderr, title+": "+text) }
func hideWindow(*exec.Cmd)             {}
func attachToJob(*os.Process)          {}
func setSystemProxy(string, int) error { return nil }
func restoreSystemProxy(string)        {}

func systemProxyOurs(int) (bool, string) { return true, "" }

func openWindow(url, profile string) *exec.Cmd {
	fmt.Println("Откройте в браузере:", url)
	return nil
}

func protectedBase() string { return "" }
func legacyWindows() bool   { return false }

func setAutostart(enable, elevated bool) error {
	if enable {
		return fmt.Errorf("автозапуск поддерживается только в Windows")
	}
	return nil
}

func openURL(u string) { fmt.Println("Откройте:", u) }

func allowLANPort(int) error { return fmt.Errorf("только в Windows") }
