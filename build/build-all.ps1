<#
.SYNOPSIS
    Полная сборка HumanGram: модуль прокси, клиент для Windows
    и клиент для Android.

.DESCRIPTION
    Последовательность:

      1. HumanGram-Proxy — DLL, .so и утилита командной строки;
      2. HumanGram Desktop — подготовка зависимостей, конфигурирование,
         сборка и упаковка в dist/HumanGram-Windows.zip;
      3. HumanGram Android — сборка APK и копирование
         в dist/HumanGram-Android.apk.

    Сеть в среде сборки нестабильна, поэтому подготовка зависимостей и
    сборка Android выполняются с повторами.

.PARAMETER ApiId
    Идентификатор приложения Telegram. Если не задан, берётся из
    переменной окружения TELEGRAM_API_ID.

.PARAMETER ApiHash
    Хеш приложения Telegram. Если не задан, берётся из переменной
    окружения TELEGRAM_API_HASH.

.PARAMETER SkipDesktop
    Пропустить сборку клиента для Windows.

.PARAMETER SkipAndroid
    Пропустить сборку клиента для Android.

.EXAMPLE
    pwsh -File build\build-all.ps1 -ApiId 123456 -ApiHash abcdef...
#>
[CmdletBinding()]
param(
    [string]$ApiId = $env:TELEGRAM_API_ID,
    [string]$ApiHash = $env:TELEGRAM_API_HASH,
    [switch]$SkipDesktop,
    [switch]$SkipAndroid
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$dist = Join-Path $root 'dist'
New-Item -ItemType Directory -Force -Path $dist | Out-Null

function Write-Step($msg) {
    Write-Host ''
    Write-Host "========== $msg ==========" -ForegroundColor Green
}

if (-not $ApiId -or -not $ApiHash) {
    throw 'Не заданы ApiId и ApiHash. Укажите их параметрами или переменными TELEGRAM_API_ID и TELEGRAM_API_HASH.'
}

Write-Step '1/3 HumanGram-Proxy'
& pwsh -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'build-proxy.ps1')
if ($LASTEXITCODE -ne 0) { throw 'Сборка HumanGram-Proxy не удалась' }

if (-not $SkipDesktop) {
    Write-Step '2/3 HumanGram Desktop'
    & pwsh -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'prepare-desktop-retry.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'Подготовка зависимостей Desktop не удалась' }
    & pwsh -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'build-desktop.ps1') `
        -ApiId $ApiId -ApiHash $ApiHash -SkipPrepare
    if ($LASTEXITCODE -ne 0) { throw 'Сборка HumanGram Desktop не удалась' }
}

if (-not $SkipAndroid) {
    Write-Step '3/3 HumanGram Android'
    & pwsh -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'build-android-retry.ps1') `
        -ApiId $ApiId -ApiHash $ApiHash
    if ($LASTEXITCODE -ne 0) { throw 'Сборка HumanGram Android не удалась' }
}

Write-Step 'Готовые артефакты'
Get-ChildItem $dist -Recurse -File -ErrorAction SilentlyContinue |
    Where-Object { $_.Extension -in '.zip', '.apk', '.dll', '.so', '.exe' } |
    Select-Object @{Name='Файл'; Expression={ $_.FullName.Replace("$root\", '') } },
                  @{Name='Размер'; Expression={ '{0:N1} МБ' -f ($_.Length / 1MB) } } |
    Format-Table -AutoSize
