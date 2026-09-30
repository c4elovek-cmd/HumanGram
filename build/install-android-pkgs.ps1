# Ручная установка компонентов Android SDK, версии которых требует
# проект Telegram Android: platforms;android-36, build-tools;36.0.0 и
# ndk;27.2.12479018 (r27b).
#
# Скрипт нужен потому, что sdkmanager теряет крупные архивы: загрузка
# выполняется через curl с возобновлением, распаковка — средствами tar.
# Все файлы размещаются на диске G.
$ErrorActionPreference = 'Stop'
$root = 'G:\HumanGram\android-sdk'
$dl   = 'G:\HumanGram\.cache\dl'
New-Item -ItemType Directory -Force -Path $root, $dl | Out-Null

function Get-Archive {
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][int]$MinMB
    )
    $path = Join-Path $dl $Name
    if ((Test-Path $path) -and ((Get-Item $path).Length / 1MB) -ge $MinMB) {
        Write-Host ("Уже скачано: {0} ({1:N1} МБ)" -f $Name, ((Get-Item $path).Length / 1MB))
        return $path
    }
    $url = "https://dl.google.com/android/repository/$Name"
    Write-Host "Загрузка $Name"
    & curl.exe -L --fail --retry 8 --retry-delay 5 --retry-all-errors -C - -o $path $url
    if ($LASTEXITCODE -ne 0) { throw "curl: код $LASTEXITCODE для $Name" }
    $mb = (Get-Item $path).Length / 1MB
    Write-Host ("Скачано {0:N1} МБ" -f $mb)
    if ($mb -lt $MinMB) { throw "$Name слишком мал ($mb МБ)" }
    return $path
}

# Распаковывает zip в целевой каталог: содержимое верхнего уровня
# (единственный каталог внутри архива) переносится в цель.
function Expand-Into {
    param(
        [Parameter(Mandatory)][string]$Zip,
        [Parameter(Mandatory)][string]$Target
    )
    if (Test-Path $Target) { Remove-Item $Target -Recurse -Force }
    New-Item -ItemType Directory -Force -Path (Split-Path $Target -Parent) | Out-Null
    $stage = Join-Path $dl ('stage-' + [System.IO.Path]::GetFileNameWithoutExtension($Zip))
    if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $stage | Out-Null
    Write-Host "Распаковка $(Split-Path $Zip -Leaf)..."
    & tar.exe -xf $Zip -C $stage
    if ($LASTEXITCODE -ne 0) { throw "tar: не удалось распаковать $Zip" }
    $inner = Get-ChildItem $stage -Directory | Select-Object -First 1
    if (-not $inner) { throw "в архиве $Zip нет каталогов" }
    Move-Item $inner.FullName $Target
    Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host "Готово: $Target"
}

# --- NDK r27b (27.2.12479018) ---
$ndkVersion = '27.2.12479018'
$ndkTarget = Join-Path $root "ndk\$ndkVersion"
if (-not (Test-Path (Join-Path $ndkTarget 'toolchains'))) {
    $zip = Get-Archive -Name 'android-ndk-r27b-windows.zip' -MinMB 700
    Expand-Into -Zip $zip -Target $ndkTarget
} else {
    Write-Host "NDK $ndkVersion уже установлен"
}

# --- build-tools 36.0.0 ---
$btTarget = Join-Path $root 'build-tools\36.0.0'
if (-not (Test-Path $btTarget)) {
    $zip = Get-Archive -Name 'build-tools_r36_windows.zip' -MinMB 40
    Expand-Into -Zip $zip -Target $btTarget
} else {
    Write-Host 'build-tools 36.0.0 уже установлен'
}

# --- platform android-36 ---
$platTarget = Join-Path $root 'platforms\android-36'
if (-not (Test-Path (Join-Path $platTarget 'android.jar'))) {
    $zip = Get-Archive -Name 'platform-36_r02.zip' -MinMB 40
    Expand-Into -Zip $zip -Target $platTarget
} else {
    Write-Host 'platform android-36 уже установлен'
}

Write-Host ''
Write-Host '=== итог ==='
foreach ($d in 'ndk', 'build-tools', 'platforms') {
    $p = Join-Path $root $d
    if (Test-Path $p) {
        Write-Host ("{0}: {1}" -f $d, ((Get-ChildItem $p -Directory | Select-Object -Expand Name) -join ', '))
    } else {
        Write-Host ("{0}: missing" -f $d)
    }
}
Write-Host ("android.jar(36): {0}" -f (Test-Path (Join-Path $platTarget 'android.jar')))
Write-Host ("aapt2(36): {0}" -f (Test-Path (Join-Path $btTarget 'aapt2.exe')))
Write-Host ("ndk clang: {0}" -f (Test-Path (Join-Path $ndkTarget 'toolchains\llvm\prebuilt\windows-x86_64\bin\aarch64-linux-android24-clang.cmd')))
