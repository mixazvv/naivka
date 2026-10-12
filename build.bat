@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"
rem Сборка Naivka. Все зависимости лежат в папке vendor — интернет не нужен.
rem   build.bat          — Windows 10/11, 64 бита (Go 1.22 или новее)
rem   build.bat debug    — то же, но с консолью для отладки
rem   build.bat win7     — Windows 7/8, 64 и 32 бита (нужен Go 1.20.x, путь к нему в GO120)
set FLAGS=-trimpath -ldflags "-H windowsgui -s -w"
if /i "%1"=="win7" goto win7
where go >nul 2>nul || (echo Go не найден. Установите его с https://go.dev/dl/ & pause & exit /b 1)
if /i "%1"=="debug" (
  go build -mod=vendor -trimpath -o Naivka-debug.exe .
) else (
  go build -mod=vendor %FLAGS% -o Naivka.exe .
)
if errorlevel 1 (echo Сборка не удалась. & pause & exit /b 1)
echo Готово.
pause
exit /b 0
:win7
if "%GO120%"=="" (echo Укажите папку Go 1.20: set GO120=C:\go1.20 & pause & exit /b 1)
set "GOROOT=%GO120%"
set GOOS=windows
set GOARCH=amd64
"%GO120%\bin\go.exe" build -modfile=go.legacy.mod -mod=mod -tags legacy %FLAGS% -o Naivka-win7.exe .
if errorlevel 1 (echo Сборка не удалась. & pause & exit /b 1)
set GOARCH=386
"%GO120%\bin\go.exe" build -modfile=go.legacy.mod -mod=mod -tags legacy %FLAGS% -o Naivka-win7-32.exe .
if errorlevel 1 (echo Сборка не удалась. & pause & exit /b 1)
echo Готово: Naivka-win7.exe и Naivka-win7-32.exe
pause
