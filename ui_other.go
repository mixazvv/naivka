//go:build !windows

package main

import "fmt"

type consoleUI struct{ a *App }

func newUI(a *App) uiHost { return &consoleUI{a: a} }

func (u *consoleUI) Run(show bool) {
	if show {
		u.Show()
	}
	select {}
}

func (u *consoleUI) Show()               { fmt.Println("Откройте в браузере:", u.a.url()) }
func (u *consoleUI) Notify(string, bool) {}
func (u *consoleUI) Refresh()            {}
func (u *consoleUI) Close()              {}
func (u *consoleUI) SetTheme(bool)       {}
func (u *consoleUI) SetLogo(logoColors)  {}
