package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Подпись exe сертификатом Authenticode — чтобы Windows SmartScreen не пугал людей
// «Система Windows защитила ваш компьютер». Подписывать необязательно: без сертификата
// всё собирается как раньше. Варианты (первый подходящий):
//
//	NAIVKA_SIGN_CMD  — своя команда, {file} заменяется на путь к exe. Для EV-сертификата на токене,
//	                   Azure Trusted Signing, SignPath и т. п., например:
//	                   signtool sign /sha1 ОТПЕЧАТОК /fd sha256 /tr http://timestamp.digicert.com /td sha256 {file}
//	NAIVKA_SIGN_CERT — файл сертификата .pfx (пароль — в NAIVKA_SIGN_PASS, не в командной строке):
//	                   на Windows подписывает signtool из Windows SDK, на Linux и macOS — osslsigncode.
//
// Метка времени нужна, чтобы подпись оставалась действительной после окончания срока сертификата.
// NAIVKA_SIGN_TS — другой сервер меток; «-» — без метки (только для проверки, не для релиза).
const defaultTimestampURL = "http://timestamp.digicert.com"

type signer struct {
	cmd      string // NAIVKA_SIGN_CMD
	cert     string // NAIVKA_SIGN_CERT
	pass     string // NAIVKA_SIGN_PASS
	signtool string
	osslsign string
	ts       string // сервер меток времени, "" — без метки
}

func newSigner() *signer {
	s := &signer{cmd: strings.TrimSpace(os.Getenv("NAIVKA_SIGN_CMD")), cert: os.Getenv("NAIVKA_SIGN_CERT"), pass: os.Getenv("NAIVKA_SIGN_PASS"),
		ts: defaultTimestampURL}
	if s.cmd == "" && s.cert == "" {
		return nil
	}
	switch v := strings.TrimSpace(os.Getenv("NAIVKA_SIGN_TS")); v {
	case "":
	case "-":
		s.ts = ""
		fmt.Println("  внимание: подпись без метки времени перестанет действовать вместе с сертификатом")
	default:
		s.ts = v
	}
	if s.cmd == "" {
		if _, err := os.Stat(s.cert); err != nil {
			die("NAIVKA_SIGN_CERT: нет файла " + s.cert)
		}
		if runtime.GOOS == "windows" {
			s.signtool = findSigntool()
			if s.signtool == "" {
				die("для подписи нужен signtool.exe из Windows SDK (https://developer.microsoft.com/windows/downloads/windows-sdk/) — или задайте NAIVKA_SIGN_CMD")
			}
		} else if p, err := exec.LookPath("osslsigncode"); err == nil {
			s.osslsign = p
		} else {
			die("для подписи на этой системе нужен osslsigncode (apt install osslsigncode / brew install osslsigncode) — или задайте NAIVKA_SIGN_CMD")
		}
	}
	return s
}

func findSigntool() string {
	if p, err := exec.LookPath("signtool.exe"); err == nil {
		return p
	}
	// Windows SDK ставит signtool в папку с номером версии — берём самую новую.
	matches, _ := filepath.Glob(filepath.Join(os.Getenv("ProgramFiles(x86)"), `Windows Kits\10\bin\10.*\x64\signtool.exe`))
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func (s *signer) sign(file string) {
	var c *exec.Cmd
	switch {
	case s.cmd != "":
		args := splitCommand(strings.ReplaceAll(s.cmd, "{file}", "\x00"))
		for i, a := range args {
			args[i] = strings.ReplaceAll(a, "\x00", file)
		}
		c = exec.Command(args[0], args[1:]...)
	case s.signtool != "":
		args := []string{"sign", "/fd", "sha256", "/f", s.cert}
		if s.ts != "" {
			args = append(args, "/tr", s.ts, "/td", "sha256")
		}
		if s.pass != "" {
			args = append(args, "/p", s.pass)
		}
		c = exec.Command(s.signtool, append(args, file)...)
	default:
		out := file + ".signed"
		args := []string{"sign", "-pkcs12", s.cert, "-h", "sha256", "-n", "Naivka", "-in", file, "-out", out}
		if s.ts != "" {
			args = append(args, "-ts", s.ts)
		}
		if s.pass != "" {
			args = append(args, "-pass", s.pass)
		}
		c = exec.Command(s.osslsign, args...)
		defer func() {
			must(os.Rename(out, file))
		}()
	}
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		die(fmt.Sprintf("не удалось подписать %s: %v", filepath.Base(file), err))
	}
	fmt.Println("  подписан " + filepath.Base(file))
}

// splitCommand делит команду на аргументы с учётом кавычек ("C:\Program Files\…").
func splitCommand(s string) []string {
	var out []string
	var cur strings.Builder
	inQ, has := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQ, has = !inQ, true
		case (r == ' ' || r == '\t') && !inQ:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		die("NAIVKA_SIGN_CMD пустая")
	}
	return out
}
