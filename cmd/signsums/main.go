// signsums — подпись релиза для автообновления Naivka в сборке на GitHub.
//
//	signsums -keygen                 новая пара ключей (личный — в секрет NAIVKA_RELEASE_KEY на GitHub)
//	signsums -pub                    открытый ключ из NAIVKA_RELEASE_KEY (вшивается в Naivka)
//	signsums -dir out -version v1.2  SHA256SUMS и SHA256SUMS.sig для файлов в папке
//
// Формат тот же, что у cmd/release: первая строка «# naivka vX.Y.Z» (защита от подсовывания
// старого подписанного релиза под новым номером), дальше «хеш  имя».
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	keygen := flag.Bool("keygen", false, "создать новую пару ключей")
	pub := flag.Bool("pub", false, "вывести открытый ключ из NAIVKA_RELEASE_KEY")
	dir := flag.String("dir", "", "папка с файлами релиза")
	version := flag.String("version", "", "версия, например v2.2.0")
	flag.Parse()

	switch {
	case *keygen:
		p, k, err := ed25519.GenerateKey(rand.Reader)
		must(err)
		fmt.Println("Личный ключ (в секрет NAIVKA_RELEASE_KEY, никому не показывать):")
		fmt.Println(base64.StdEncoding.EncodeToString(k))
		fmt.Println("Открытый ключ:")
		fmt.Println(base64.StdEncoding.EncodeToString(p))
	case *pub:
		k, err := loadKey()
		must(err)
		fmt.Println(base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey)))
	case *dir != "":
		if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(*version) {
			die("укажите -version в виде v1.2.3")
		}
		k, err := loadKey()
		must(err)
		sums, err := buildSums(*dir, *version)
		must(err)
		must(os.WriteFile(filepath.Join(*dir, "SHA256SUMS"), sums, 0o644))
		sig := ed25519.Sign(k, sums)
		if !ed25519.Verify(k.Public().(ed25519.PublicKey), sums, sig) {
			die("подпись не сходится — ключ повреждён")
		}
		must(os.WriteFile(filepath.Join(*dir, "SHA256SUMS.sig"), []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644))
		fmt.Print(string(sums))
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func loadKey() (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("NAIVKA_RELEASE_KEY")))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, errors.New("NAIVKA_RELEASE_KEY не задан или повреждён")
	}
	return ed25519.PrivateKey(raw), nil
}

func buildSums(dir, version string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), "SHA256SUMS") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var b bytes.Buffer
	fmt.Fprintf(&b, "# naivka %s\n", version)
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		h := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h[:]), n)
	}
	return b.Bytes(), nil
}

func must(err error) {
	if err != nil {
		die(err.Error())
	}
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, "signsums: "+msg)
	os.Exit(1)
}
