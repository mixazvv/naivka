package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Манифест для winget (https://github.com/microsoft/winget-pkgs). Ставится архив
// Naivka-winget.zip — без portable.txt: настройки живут в %APPDATA%\Naivka и переживают
// обновления через winget (папку программы winget при обновлении заменяет целиком).
// Ядро sing-box Naivka скачает сама при первом запуске.
//
// Бонус: файлы, которые поставил winget, не помечены как «скачанные из интернета»,
// поэтому SmartScreen не показывает предупреждение даже без подписи exe.

func wingetFiles(repo, version, zipPath, outDir string) {
	b, err := os.ReadFile(zipPath)
	must(err)
	h := sha256.Sum256(b)
	owner := strings.Split(repo, "/")[0]
	id := owner + ".Naivka"
	v := strings.TrimPrefix(version, "v")
	dir := filepath.Join(outDir, "manifests", strings.ToLower(owner[:1]), owner, "Naivka", v)
	must(os.MkdirAll(dir, 0o755))
	head := "# Создано утилитой выпуска Naivka. Схема: https://aka.ms/winget-manifest.schema.json\n"
	write := func(name, body string) { must(os.WriteFile(filepath.Join(dir, id+name), []byte(head+body), 0o644)) }
	write(".yaml", fmt.Sprintf(`PackageIdentifier: %s
PackageVersion: %s
DefaultLocale: ru-RU
ManifestType: version
ManifestVersion: 1.6.0
`, id, v))
	write(".installer.yaml", fmt.Sprintf(`PackageIdentifier: %s
PackageVersion: %s
InstallerType: zip
NestedInstallerType: portable
NestedInstallerFiles:
  - RelativeFilePath: Naivka\Naivka.exe
    PortableCommandAlias: naivka
MinimumOSVersion: 10.0.17763.0
ReleaseDate: %s
Installers:
  - Architecture: x64
    InstallerUrl: https://github.com/%s/releases/download/%s/%s
    InstallerSha256: %s
ManifestType: installer
ManifestVersion: 1.6.0
`, id, v, time.Now().Format("2006-01-02"), repo, version, filepath.Base(zipPath), strings.ToUpper(hex.EncodeToString(h[:]))))
	write(".locale.ru-RU.yaml", fmt.Sprintf(`PackageIdentifier: %s
PackageVersion: %s
PackageLocale: ru-RU
Publisher: %s
PublisherUrl: https://github.com/%s
PackageName: Naivka
PackageUrl: https://github.com/%s
License: Proprietary
ShortDescription: Клиент NaiveProxy, VLESS+Reality, Hysteria2 и других протоколов для Windows на основе sing-box.
Tags:
  - proxy
  - sing-box
  - naiveproxy
  - vless
ManifestType: defaultLocale
ManifestVersion: 1.6.0
`, id, v, owner, owner, repo))
	fmt.Printf("  манифест winget: %s\n", dir)
}
