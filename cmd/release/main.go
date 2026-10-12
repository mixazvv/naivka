// Утилита выпуска новой версии Naivka.
//
//	go run ./cmd/release -version v1.0.1
//
// Собирает Naivka для Windows 10/11 (и Windows 7/8, если задан GO120), вшивает версию,
// репозиторий и открытый ключ, подписывает контрольные суммы личным ключом и складывает
// всё для релиза на GitHub в папку dist/<версия>. С -publish сразу публикует релиз через gh.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

type config struct {
	Repo string `json:"repo"`
}

func main() {
	version := flag.String("version", "", "номер версии, например v1.0.1")
	repo := flag.String("repo", "", "репозиторий на GitHub, например ivan/naivka (запоминается)")
	go120 := flag.String("go120", os.Getenv("GO120"), "папка Go 1.20 для сборки под Windows 7/8 (можно через переменную GO120)")
	notes := flag.String("notes", "CHANGES.md", "файл со списком изменений")
	publish := flag.Bool("publish", false, "сразу опубликовать релиз через GitHub CLI (gh)")
	flag.Parse()

	if _, err := os.Stat("go.mod"); err != nil {
		die("запускайте из корня проекта Naivka (там, где go.mod)")
	}
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(*version) {
		die("укажите версию в виде v1.0.1: go run ./cmd/release -version v1.0.1")
	}

	cfg := config{}
	if b, err := os.ReadFile("release.json"); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	if *repo != "" {
		cfg.Repo = strings.Trim(strings.TrimPrefix(*repo, "https://github.com/"), "/")
		b, _ := json.MarshalIndent(cfg, "", "  ")
		_ = os.WriteFile("release.json", b, 0o644)
	}
	if !regexp.MustCompile(`^[\w.-]+/[\w.-]+$`).MatchString(cfg.Repo) {
		die("укажите репозиторий один раз: go run ./cmd/release -repo ИМЯ/naivka -version " + *version)
	}

	priv, pub := loadKeys()
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	out := filepath.Join("dist", *version)
	if err := os.MkdirAll(out, 0o755); err != nil {
		die(err.Error())
	}
	ld := fmt.Sprintf("-s -w -X main.appVersion=%s -X main.selfRepo=%s -X main.updateKey=%s", *version, cfg.Repo, pubB64)

	step("Сборка для Windows 10/11")
	build("go", nil, []string{"build", "-mod=vendor", "-trimpath", "-ldflags", "-H windowsgui " + ld, "-o", filepath.Join(out, "Naivka.exe"), "."}, "amd64")
	build("go", nil, []string{"build", "-mod=vendor", "-trimpath", "-ldflags", ld, "-o", filepath.Join(out, "Naivka-debug.exe"), "."}, "amd64")
	win7 := false
	if *go120 != "" {
		step("Сборка для Windows 7/8 (Go 1.20)")
		gobin := filepath.Join(*go120, "bin", "go"+exeExt())
		env := []string{"GOROOT=" + *go120, "GOFLAGS="}
		for _, arch := range []string{"amd64", "386"} {
			name := map[string]string{"amd64": "Naivka-win7-64.exe", "386": "Naivka-win7-32.exe"}[arch]
			build(gobin, env, []string{"build", "-modfile=go.legacy.mod", "-mod=mod", "-tags", "legacy", "-trimpath",
				"-ldflags", "-H windowsgui " + ld, "-o", filepath.Join(out, name), "."}, arch)
		}
		win7 = true
	} else {
		fmt.Println("  Windows 7/8 пропущена: не задан GO120 (папка Go 1.20). У старых пользователей версия не обновится.")
	}

	step("Список изменений")
	changes := "Naivka " + *version
	if b, err := os.ReadFile(*notes); err == nil {
		changes = strings.TrimSpace(string(b))
	} else {
		fmt.Println("  файла " + *notes + " нет — в окне обновления будет только номер версии")
	}
	must(os.WriteFile(filepath.Join(out, "CHANGES.txt"), []byte(changes+"\n"), 0o644))

	step("Ядро sing-box для архивов")
	tag := latestTag("SagerNet/sing-box")
	ver := strings.TrimPrefix(tag, "v")
	core := fetchZip(fmt.Sprintf("https://github.com/SagerNet/sing-box/releases/download/%s/sing-box-%s-windows-amd64.zip", tag, ver))
	fmt.Println("  sing-box " + tag)

	step("Архивы для людей")
	portable := map[string][]byte{
		"Naivka/Naivka.exe":                            read(filepath.Join(out, "Naivka.exe")),
		"Naivka/Naivka-debug.exe":                      read(filepath.Join(out, "Naivka-debug.exe")),
		"Naivka/portable.txt":                          crlf(portableTxt),
		"Naivka/README.txt":                            crlf(strings.ReplaceAll(readmeModern, "{V}", *version)),
		"Naivka/Naivka-data/core/sing-box.exe":         core["sing-box.exe"],
		"Naivka/Naivka-data/core/libcronet.dll":        core["libcronet.dll"],
		"Naivka/Naivka-data/core/LICENSE-sing-box.txt": core["LICENSE"],
	}
	writeZip(filepath.Join(out, "Naivka-portable.zip"), portable)
	if win7 {
		w7 := map[string][]byte{}
		for arch, dir := range map[string]string{"amd64": "Naivka-win7-64", "386": "Naivka-win7-32"} {
			c := fetchZip(fmt.Sprintf("https://github.com/SagerNet/sing-box/releases/download/%s/sing-box-%s-windows-%s-legacy-windows-7.zip", tag, ver, arch))
			exe := map[string]string{"amd64": "Naivka-win7-64.exe", "386": "Naivka-win7-32.exe"}[arch]
			w7[dir+"/Naivka.exe"] = read(filepath.Join(out, exe))
			w7[dir+"/portable.txt"] = crlf(portableTxt)
			w7[dir+"/README.txt"] = crlf(strings.ReplaceAll(readmeLegacy, "{V}", *version))
			w7[dir+"/Naivka-data/core/sing-box.exe"] = c["sing-box.exe"]
			w7[dir+"/Naivka-data/core/LICENSE-sing-box.txt"] = c["LICENSE"]
		}
		writeZip(filepath.Join(out, "Naivka-win7.zip"), w7)
	}

	step("Контрольные суммы и подпись")
	entries, _ := os.ReadDir(out)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), "SHA256SUMS") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var sums bytes.Buffer
	for _, n := range names {
		h := sha256.Sum256(read(filepath.Join(out, n)))
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), n)
	}
	must(os.WriteFile(filepath.Join(out, "SHA256SUMS"), sums.Bytes(), 0o644))
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums.Bytes()))
	must(os.WriteFile(filepath.Join(out, "SHA256SUMS.sig"), []byte(sig+"\n"), 0o644))
	if !ed25519.Verify(pub, sums.Bytes(), ed25519.Sign(priv, sums.Bytes())) {
		die("подпись не сходится — ключи повреждены")
	}

	if *publish {
		step("Публикация на GitHub")
		files, _ := filepath.Glob(filepath.Join(out, "*"))
		args := append([]string{"release", "create", *version, "--repo", cfg.Repo, "--title", "Naivka " + *version,
			"--notes-file", filepath.Join(out, "CHANGES.txt")}, files...)
		cmd := exec.Command("gh", args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			die("gh не смог опубликовать релиз: " + err.Error() + "\nОпубликуйте вручную — шаги ниже.")
		}
		fmt.Printf("\nГотово: https://github.com/%s/releases/tag/%s\n", cfg.Repo, *version)
		return
	}
	fmt.Printf(`
Готово. Файлы релиза — в папке %s

Опубликуйте их:
  1. Откройте https://github.com/%s/releases/new
  2. «Choose a tag» → впишите %s → «Create new tag».
  3. Заголовок: Naivka %s. В описание можно вставить текст из CHANGES.txt.
  4. Перетащите в поле файлов ВСЁ из папки %s (включая SHA256SUMS и SHA256SUMS.sig).
  5. «Publish release».

После этого у всех, у кого стоит Naivka, в течение 12 часов появится предложение обновиться.
`, out, cfg.Repo, *version, *version, out)
}

// loadKeys: личный ключ подписи создаётся один раз. Без него нельзя выпускать обновления,
// а у кого он окажется — тот сможет их подделать. Храните копию в надёжном месте.
func loadKeys() (ed25519.PrivateKey, ed25519.PublicKey) {
	_ = os.MkdirAll("keys", 0o700)
	if b, err := os.ReadFile(filepath.Join("keys", "release.key")); err == nil {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(raw) != ed25519.PrivateKeySize {
			die("keys/release.key повреждён")
		}
		priv := ed25519.PrivateKey(raw)
		return priv, priv.Public().(ed25519.PublicKey)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	must(err)
	must(os.WriteFile(filepath.Join("keys", "release.key"), []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600))
	must(os.WriteFile(filepath.Join("keys", "release.pub"), []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644))
	fmt.Println(`
ВНИМАНИЕ: создан ключ подписи релизов keys/release.key.
  Сохраните его копию в надёжном месте (флешка, менеджер паролей). Если он потеряется,
  уже установленные Naivka не примут новые обновления — людям придётся скачать её заново.
  Никому его не отдавайте и не загружайте на GitHub: с ним можно подделать обновление.`)
	return priv, pub
}

func build(gobin string, extraEnv, args []string, arch string) {
	cmd := exec.Command(gobin, args...)
	cmd.Env = append(append(os.Environ(), "GOOS=windows", "GOARCH="+arch, "CGO_ENABLED=0"), extraEnv...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		die("сборка не удалась: " + strings.Join(args, " "))
	}
	fmt.Println("  " + args[len(args)-2])
}

func latestTag(repo string) string {
	hc := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := hc.Head("https://github.com/" + repo + "/releases/latest")
	if err != nil {
		die("GitHub недоступен: " + err.Error())
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		die("не удалось узнать версию " + repo)
	}
	return loc[i+5:]
}

// fetchZip скачивает архив и достаёт файлы по именам (без папок).
func fetchZip(url string) map[string][]byte {
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Get(url)
	if err != nil || resp.StatusCode != 200 {
		die("не удалось скачать " + url)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	must(err)
	out := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		must(err)
		data, _ := io.ReadAll(rc)
		rc.Close()
		out[filepath.Base(f.Name)] = data
	}
	return out
}

func writeZip(path string, files map[string][]byte) {
	f, err := os.Create(path)
	must(err)
	zw := zip.NewWriter(f)
	var names []string
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if files[n] == nil {
			die("нет файла для архива: " + n)
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: time.Now()})
		must(err)
		_, err = w.Write(files[n])
		must(err)
	}
	must(zw.Close())
	must(f.Close())
	fmt.Println("  " + filepath.Base(path))
}

func read(p string) []byte {
	b, err := os.ReadFile(p)
	must(err)
	return b
}

func crlf(s string) []byte {
	return []byte(strings.ReplaceAll(strings.TrimLeft(s, "\n"), "\n", "\r\n"))
}

func exeExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func step(s string) { fmt.Println("\n== " + s) }

func must(err error) {
	if err != nil {
		die(err.Error())
	}
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, "Ошибка: "+msg)
	os.Exit(1)
}

const portableTxt = `
Этот файл включает портативный режим: настройки, серверы и ядро хранятся
в папке Naivka-data рядом с программой. Удалите его, чтобы хранить данные в %APPDATA%\Naivka.
`

const readmeModern = `
Naivka {V} — для Windows 10 и 11 (64 бита)

Запуск: Naivka.exe. Ничего устанавливать не нужно, ядро sing-box уже в Naivka-data\core.

1. Если Windows покажет «Система Windows защитила ваш компьютер» — программа не подписана:
   «Подробнее» → «Выполнить в любом случае».
2. «Серверы» → вставьте ссылку на свой сервер (naive+https://… или vless://…).
3. «Обзор» → «Подключить».

Закрытие окна убирает Naivka в трей (значок у часов). Выход — правый клик по значку → «Выход».
Новые версии Naivka предложит сама: «Настройки» → «О программе».
Все данные лежат в Naivka-data: папку можно переносить на флешке.
sing-box — GPL-3.0, https://github.com/SagerNet/sing-box (лицензия в Naivka-data\core).
`

const readmeLegacy = `
Naivka {V} — для Windows 7 и 8

Запуск: Naivka.exe. Ядро sing-box для Windows 7 уже в Naivka-data\core.

На Windows 7 работают серверы VLESS (Reality, подписка «белые списки», xHTTP через Xray).
Naive здесь невозможен: его движок больше не поддерживает Windows 7.
Окно Naivka открывается в браузере. Закрыли окно — Naivka осталась в трее у часов.
Новые версии Naivka предложит сама: «Настройки» → «О программе».
sing-box — GPL-3.0, https://github.com/SagerNet/sing-box (лицензия в Naivka-data\core).
`
