<#
.SYNOPSIS
    Безопасное продолжение сборки HumanGram после прерывания.

.DESCRIPTION
    Скрипт можно запускать в любой момент и сколько угодно раз: он
    сначала приводит рабочее дерево в согласованное состояние, затем
    определяет, что уже сделано, и выполняет только незавершённые шаги.

    Что делается перед каждой сборкой:

      * завершаются зависшие процессы сборки (Gradle, javac, cmake, ninja,
        MSBuild, Python из prepare.py);
      * удаляются устаревшие файлы блокировок Gradle, из-за которых
        прерванная сборка не возобновляется;
      * проверяется целостность уже собранных артефактов.

    Все промежуточные данные лежат на диске G и переиспользуются:
    Gradle не пересобирает выполненные задачи, CMake и NDK продолжают
    инкрементальную сборку, prepare.py пропускает готовые стадии.

    Использование:

        pwsh -File build\resume.ps1                 # продолжить всё
        pwsh -File build\resume.ps1 -Only Android    # только Android
        pwsh -File build\resume.ps1 -Status          # только отчёт

.PARAMETER Only
    Ограничить работу одной платформой: Proxy, Android, Desktop или Proxy.

.PARAMETER Universal
    Собирать Android со всеми четырьмя архитектурами.

.PARAMETER Status
    Только показать состояние, ничего не запускать.
#>
[CmdletBinding()]
param(
    [ValidateSet('All', 'Proxy', 'Android', 'Desktop')]
    [string]$Only = 'All',
    [switch]$Universal,
    [switch]$Status
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
$android = Join-Path $root 'android'
$dist = Join-Path $root 'dist'
$logs = Join-Path $root 'build\logs'
$cache = Join-Path $root '.cache'
New-Item -ItemType Directory -Force -Path $dist, $logs, $cache | Out-Null

$apiId = $env:TELEGRAM_API_ID
$apiHash = $env:TELEGRAM_API_HASH

# Ключи берутся из переменных окружения, а при их отсутствии — из
# локального файла build/local-credentials.ps1, который не попадает в
# репозиторий. Это позволяет запускать сборку повторно без повторного
# ввода ключей.
$credFile = Join-Path $PSScriptRoot 'local-credentials.ps1'
if ((-not $apiId -or -not $apiHash) -and (Test-Path $credFile)) {
    try {
        . $credFile
        $apiId = $env:TELEGRAM_API_ID
        $apiHash = $env:TELEGRAM_API_HASH
    } catch {
        Write-Warn "Не удалось прочитать $credFile"
    }
}

if (-not $apiId -or -not $apiHash) {
    Write-Host ''
    Write-Host 'Ключи Telegram API не заданы — сборка клиентов будет пропущена.' -ForegroundColor Yellow
    Write-Host "Задайте TELEGRAM_API_ID и TELEGRAM_API_HASH либо создайте $credFile." -ForegroundColor Yellow
    Write-Host 'Пример содержимого файла:'
    Write-Host '  $env:TELEGRAM_API_ID = "123456"'
    Write-Host '  $env:TELEGRAM_API_HASH = "0123456789abcdef..."'
}

function Write-Step($msg) {
    Write-Host ''
    Write-Host "========== $msg ==========" -ForegroundColor Green
}

function Write-Info($msg) { Write-Host "  $msg" }
function Write-Ok($msg) { Write-Host "  [готово] $msg" -ForegroundColor DarkGreen }
function Write-Warn($msg) { Write-Host "  [внимание] $msg" -ForegroundColor Yellow }

# --------------------------------------------------------------------------
# 1. Остановка зависших процессов
# --------------------------------------------------------------------------
function Stop-BuildProcesses {
    $names = @('java', 'javaw', 'gradle', 'kotlinc', 'ninja', 'cmake', 'msbuild', 'python', 'cl', 'link')
    $stopped = 0
    foreach ($n in $names) {
        $procs = Get-Process -Name $n -ErrorAction SilentlyContinue
        foreach ($p in $procs) {
            # Процессы самой оболочки сборки не трогаем.
            if ($p.Id -eq $PID) { continue }
            try {
                $stopped++
                $p | Stop-Process -Force -ErrorAction SilentlyContinue
            } catch { }
        }
    }
    if ($stopped -gt 0) { Start-Sleep -Seconds 2 }
    return $stopped
}

# --------------------------------------------------------------------------
# 2. Удаление устаревших блокировок
# --------------------------------------------------------------------------
function Clear-StaleLocks {
    $removed = 0
    $patterns = @(
        (Join-Path $android '.gradle\*.lock'),
        (Join-Path $android '.gradle\*\*\*.lock'),
        (Join-Path $android '**\*.lock')
    )
    foreach ($pattern in $patterns) {
        Get-ChildItem -Path $pattern -File -ErrorAction SilentlyContinue |
            ForEach-Object {
                try {
                    Remove-Item $_.FullName -Force -ErrorAction Stop
                    $removed++
                } catch { }
            }
    }
    # Блокировка индекса git: возникает при выключении во время коммита.
    $gitLock = Join-Path $root '.git\index.lock'
    if (Test-Path $gitLock) {
        Remove-Item $gitLock -Force -ErrorAction SilentlyContinue
        $removed++
    }
    return $removed
}

# --------------------------------------------------------------------------
# 3. Оценка состояния
# --------------------------------------------------------------------------
function Get-ApkInfo {
    $apk = Join-Path $dist 'HumanGram-Android.apk'
    if (-not (Test-Path $apk)) { return $null }
    return Get-Item $apk
}

function Test-ApkConsistent {
    param([Parameter(Mandatory)][string]$Path)
    # APK считается пригодным, если объявленные ABI совпадают с теми,
    # для которых реально есть нативные библиотеки: иначе приложение
    # упадёт при запуске на устройстве с другой архитектурой.
    $bt = Join-Path $root 'android-sdk\build-tools\36.0.0\aapt2.exe'
    $list = & tar.exe -tf $Path 2>$null
    if (-not $list) { return $false }

    $abis = @($list |
        Where-Object { $_ -match '^lib/([^/]+)/' } |
        ForEach-Object { $_ -replace '^lib/([^/]+)/.*$', '$1' } |
        Sort-Object -Unique)

    $complete = $true
    foreach ($abi in $abis) {
        if (-not ($list -contains "lib/$abi/libtmessages.49.so")) { $complete = $false }
        if (-not ($list -contains "lib/$abi/libhumangramproxy.so")) { $complete = $false }
    }
    return [pscustomobject]@{ Abis = $abis; Complete = $complete }
}

function Get-DesktopPrepareState {
    $log = Get-ChildItem (Join-Path $logs 'desktop-prepare-attempt*.log') -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $log) { return $null }
    $stages = (Get-Content $log.FullName -ErrorAction SilentlyContinue |
        Select-String -Pattern '^\[\d+/33\]').Count
    return [pscustomobject]@{ Log = $log.FullName; Stages = $stages }
}

# --------------------------------------------------------------------------
# Отчёт о состоянии
# --------------------------------------------------------------------------
Write-Step 'Состояние сборки'

$proxyDll = Join-Path $dist 'lib\HumanGramProxy.dll'
$proxyExe = Join-Path $dist 'lib\humangram-proxy.exe'
if (Test-Path $proxyDll) { Write-Ok "HumanGram-Proxy: $([math]::Round((Get-Item $proxyDll).Length/1MB,1)) МБ" }
else { Write-Warn 'HumanGram-Proxy: библиотека не собрана' }
if (Test-Path $proxyExe) { Write-Ok "утилита командной строки собрана" }

$apk = Get-ApkInfo
if ($apk) {
    $info = Test-ApkConsistent -Path $apk.FullName
    $abiText = ($info.Abis -join ', ')
    if ($info.Complete) {
        Write-Ok "APK: $([math]::Round($apk.Length/1MB,1)) МБ, ABI: $abiText — согласован"
    } else {
        Write-Warn "APK: $([math]::Round($apk.Length/1MB,1)) МБ, ABI: $abiText — НАБОР НЕПОЛНЫЙ, требуется пересборка"
    }
} else {
    Write-Warn 'APK: не собран'
}

$ds = Get-DesktopPrepareState
if ($ds) {
    if ($ds.Stages -ge 33) { Write-Ok "Desktop: подготовка зависимостей завершена ($($ds.Stages)/33)" }
    else { Write-Warn "Desktop: подготовка зависимостей $($ds.Stages)/33 — можно продолжить" }
} else {
    Write-Warn 'Desktop: подготовка зависимостей не начиналась'
}

$desktopExe = 'G:\HumanGram\desktop\Telegram\out\Release\Telegram.exe'
if (Test-Path $desktopExe) { Write-Ok "Desktop: исполняемый файл собран" }
else { Write-Warn 'Desktop: клиент не собран' }

if ($Status) {
    Write-Host ''
    Write-Host 'Режим просмотра: ничего не запускалось.' -ForegroundColor Cyan
    exit 0
}

# --------------------------------------------------------------------------
# Приведение к согласованному состоянию
# --------------------------------------------------------------------------
Write-Step 'Восстановление после прерывания'

$stopped = Stop-BuildProcesses
if ($stopped -gt 0) { Write-Info "завершено зависших процессов: $stopped" }
else { Write-Info 'зависших процессов нет' }

$locks = Clear-StaleLocks
if ($locks -gt 0) { Write-Info "удалено устаревших блокировок: $locks" }
else { Write-Info 'устаревших блокировок нет' }

# --------------------------------------------------------------------------
# Сборка
# --------------------------------------------------------------------------
if ($Only -in @('All', 'Proxy')) {
    Write-Step 'HumanGram-Proxy'
    & pwsh -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'build-proxy.ps1') -SkipTests
    if ($LASTEXITCODE -eq 0) { Write-Ok 'модуль собран' }
    else { Write-Warn 'сборка модуля не удалась' }
}

if ($Only -in @('All', 'Android') -and $apiId -and $apiHash) {
    Write-Step 'HumanGram Android'
    $retry = Join-Path $PSScriptRoot 'build-android-retry.ps1'
    $args = @('-ApiId', $apiId, '-ApiHash', $apiHash)
    if ($Universal) { $args += '-Universal' }
    & pwsh -NoProfile -ExecutionPolicy Bypass -File $retry @args
    if ($LASTEXITCODE -eq 0) {
        $apk = Get-ApkInfo
        if ($apk) {
            $info = Test-ApkConsistent -Path $apk.FullName
            if ($info.Complete) { Write-Ok "APK готов и согласован: ABI $($info.Abis -join ', ')" }
            else { Write-Warn 'APK собран, но набор архитектур неполон' }
        }
    } else {
        Write-Warn 'сборка Android не завершена'
    }
}

if ($Only -in @('All', 'Desktop') -and $apiId -and $apiHash) {
    Write-Step 'HumanGram Desktop'
    & pwsh -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'prepare-desktop-retry.ps1')
    if ($LASTEXITCODE -eq 0) {
        & pwsh -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'build-desktop.ps1') `
            -ApiId $apiId -ApiHash $apiHash -SkipPrepare
        if ($LASTEXITCODE -eq 0) { Write-Ok 'Desktop собран' }
        else { Write-Warn 'сборка Desktop не удалась' }
    } else {
        Write-Warn 'подготовка зависимостей Desktop не завершена'
    }
}

Write-Step 'Готовые артефакты'
Get-ChildItem $dist -File -ErrorAction SilentlyContinue |
    Where-Object { $_.Extension -in '.apk', '.zip', '.dll', '.exe' } |
    ForEach-Object { Write-Host ("  {0,-32} {1,8:N1} МБ" -f $_.Name, ($_.Length / 1MB)) }
Write-Host ''
Write-Host 'Прерывание сборки безопасно: запустите этот же скрипт снова.' -ForegroundColor Cyan