<#
.SYNOPSIS
    Сборка HumanGram Desktop: клиента Telegram Desktop со встроенной
    библиотекой HumanGram-Proxy.

.DESCRIPTION
    Скрипт выполняет полный цикл:

      1. проверяет наличие Visual Studio Build Tools, CMake, Ninja, Python;
      2. при необходимости запускает подготовку зависимостей
         (Telegram/build/prepare/win.bat silent);
      3. конфигурирует проект с идентификаторами приложения Telegram;
      4. собирает конфигурацию Release;
      5. переименовывает исполняемый файл в HumanGram.exe;
      6. упаковывает содержимое в dist/HumanGram-Windows.zip.

    Идентификаторы передаются в CMake как параметры и не сохраняются
    в репозитории.

.PARAMETER ApiId
    Идентификатор приложения Telegram (получить на my.telegram.org).
    Если не задан, берётся из переменной окружения TELEGRAM_API_ID.

.PARAMETER ApiHash
    Хеш приложения Telegram. Если не задан, берётся из переменной
    окружения TELEGRAM_API_HASH.

.PARAMETER SkipPrepare
    Не запускать подготовку зависимостей (использовать уже подготовленные).

.PARAMETER Configuration
    Конфигурация сборки: Release (по умолчанию) или Debug.

.EXAMPLE
    pwsh -File build\build-desktop.ps1 -ApiId 123456 -ApiHash abcdef...
#>
[CmdletBinding()]
param(
    [string]$ApiId = $env:TELEGRAM_API_ID,
    [string]$ApiHash = $env:TELEGRAM_API_HASH,
    [ValidateSet('Release', 'Debug')]
    [string]$Configuration = 'Release',
    [switch]$SkipPrepare
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$desktop = Join-Path $root 'desktop'
$telegram = Join-Path $desktop 'Telegram'
$dist = Join-Path $root 'dist'
$logs = Join-Path $root 'build\logs'
New-Item -ItemType Directory -Force -Path $dist, $logs | Out-Null

$vcvars = 'C:\Program Files (x86)\Microsoft Visual Studio\18\BuildTools\VC\Auxiliary\Build\vcvars64.bat'

function Write-Step($msg) {
    Write-Host ''
    Write-Host "==> $msg" -ForegroundColor Cyan
}

# Команда, выполняемая внутри окружения Visual Studio.
function Invoke-InVs([string]$command, [string]$logPath) {
    $full = "call `"$vcvars`" && cd /d `"$telegram`" && $command"
    if ($logPath) {
        $full += " > `"$logPath`" 2>&1"
    }
    & cmd /c $full
    return $LASTEXITCODE
}

Write-Step 'Проверка окружения'
if (-not (Test-Path $vcvars)) { throw "Не найден Visual Studio Build Tools: $vcvars" }
$cmake = Get-Command cmake -ErrorAction SilentlyContinue
if (-not $cmake) { throw 'Не найден CMake. Установите Kitware.CMake.' }
$ninja = Get-Command ninja -ErrorAction SilentlyContinue
if (-not $ninja) { throw 'Не найден Ninja. Установите Ninja-build.Ninja.' }
$python = Get-Command python -ErrorAction SilentlyContinue
if (-not $python) { throw 'Не найден Python 3.' }
Write-Host "cmake: $((& cmake --version) -join ' ')"
Write-Host "ninja: $(& ninja --version)"
Write-Host "python: $(& python --version)"

if (-not $ApiId -or -not $ApiHash) {
    throw 'Не заданы ApiId и ApiHash. Укажите их параметрами или переменными TELEGRAM_API_ID и TELEGRAM_API_HASH.'
}

if (-not $SkipPrepare) {
    Write-Step 'Подготовка зависимостей (Telegram/build/prepare)'
    Write-Host 'Загрузка и сборка сторонних библиотек занимает существенное время.'
    $prepareLog = Join-Path $logs 'desktop-prepare.log'
    $rc = Invoke-InVs 'build\prepare\win.bat silent' $prepareLog
    if ($rc -ne 0) {
        Write-Host 'Подготовка зависимостей завершилась с ошибкой. Последние строки журнала:' -ForegroundColor Red
        Get-Content $prepareLog -Tail 40
        throw "prepare завершился с кодом $rc"
    }
    Write-Host 'Зависимости подготовлены.'
}

Write-Step "Конфигурирование ($Configuration, x64)"
$configureLog = Join-Path $logs 'desktop-configure.log'
$configureCmd = "build\configure.bat x64 -D TDESKTOP_API_ID=$ApiId -D TDESKTOP_API_HASH=$ApiHash"
$rc = Invoke-InVs $configureCmd $configureLog
if ($rc -ne 0) {
    Get-Content $configureLog -Tail 50
    throw "configure завершился с кодом $rc"
}
Write-Host "Конфигурация создана: $telegram\out"

Write-Step "Сборка $Configuration (это занимает 1-3 часа)"
$buildLog = Join-Path $logs 'desktop-build.log'
$buildCmd = "cmake --build out --config $Configuration --target Telegram"
$rc = Invoke-InVs $buildCmd $buildLog
if ($rc -ne 0) {
    Write-Host 'Сборка завершилась с ошибкой. Последние строки журнала:' -ForegroundColor Red
    Get-Content $buildLog -Tail 60
    throw "сборка завершилась с кодом $rc"
}

Write-Step 'Результат'
$outDir = Join-Path $telegram "out\$Configuration"
$exe = Join-Path $outDir 'Telegram.exe'
if (-not (Test-Path $exe)) { throw "Не найден исполняемый файл $exe" }

$humanExe = Join-Path $outDir 'HumanGram.exe'
Copy-Item $exe $humanExe -Force
Write-Host ("Telegram.exe -> HumanGram.exe ({0:N1} МБ)" -f ((Get-Item $humanExe).Length / 1MB))

$zip = Join-Path $dist 'HumanGram-Windows.zip'
Remove-Item $zip -Force -ErrorAction SilentlyContinue
Compress-Archive -Path (Join-Path $outDir '*') -DestinationPath $zip -CompressionLevel Optimal
Write-Host ("Упаковано: {0} ({1:N1} МБ)" -f $zip, ((Get-Item $zip).Length / 1MB))
Get-Item $zip | Select-Object Name, Length, LastWriteTime
