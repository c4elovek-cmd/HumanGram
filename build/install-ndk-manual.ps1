# Ручная установка Android NDK: загрузка крупных архивов через curl
# с возобновлением и последующей распаковкой средствами tar.
# Скрипт нужен потому, что sdkmanager теряет крупные загрузки.
$ErrorActionPreference = 'Stop'
$root = 'G:\HumanGram\android-sdk'
$dl   = 'G:\HumanGram\.cache\dl'
New-Item -ItemType Directory -Force -Path $root, $dl | Out-Null

function Get-LargeFile {
    param(
        [Parameter(Mandatory)][string]$Url,
        [Parameter(Mandatory)][string]$Path,
        [int]$MinMB = 100
    )
    if ((Test-Path $Path) -and ((Get-Item $Path).Length / 1MB) -ge $MinMB) {
        Write-Host ("Уже скачано: {0} ({1:N1} МБ)" -f (Split-Path $Path -Leaf), ((Get-Item $Path).Length / 1MB))
        return
    }
    Write-Host "Загрузка $Url"
    # --retry и -C - обеспечивают возобновление при обрыве соединения.
    & curl.exe -L --fail --retry 8 --retry-delay 5 --retry-all-errors -C - -o $Path $Url
    if ($LASTEXITCODE -ne 0) { throw "curl завершился с кодом $LASTEXITCODE" }
    $size = (Get-Item $Path).Length / 1MB
    Write-Host ("Скачано {0:N1} МБ" -f $size)
    if ($size -lt $MinMB) { throw "файл слишком мал ($size МБ): загрузка повреждена" }
}

# --- NDK r27 ---
$ndkVersion = '27.0.12077973'
$ndkZip = Join-Path $dl 'android-ndk-r27-windows.zip'
Get-LargeFile -Url 'https://dl.google.com/android/repository/android-ndk-r27-windows.zip' -Path $ndkZip -MinMB 700

$ndkTarget = Join-Path $root "ndk\$ndkVersion"
if (-not (Test-Path (Join-Path $ndkTarget 'toolchains'))) {
    Write-Host 'Распаковка NDK...'
    if (Test-Path $ndkTarget) { Remove-Item $ndkTarget -Recurse -Force }
    $stage = Join-Path $dl 'ndk-stage'
    if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $stage | Out-Null
    & tar.exe -xf $ndkZip -C $stage
    if ($LASTEXITCODE -ne 0) { throw 'распаковка NDK не удалась' }
    # Архив содержит каталог android-ndk-r27 верхнего уровня.
    $inner = Join-Path $stage 'android-ndk-r27'
    if (-not (Test-Path $inner)) {
        $inner = (Get-ChildItem $stage -Directory | Select-Object -First 1).FullName
    }
    New-Item -ItemType Directory -Force -Path (Split-Path $ndkTarget -Parent) | Out-Null
    Move-Item $inner $ndkTarget
    Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue
}

$clang = Join-Path $ndkTarget 'toolchains\llvm\prebuilt\windows-x86_64\bin\aarch64-linux-android24-clang.cmd'
if (-not (Test-Path $clang)) {
    $clang = Join-Path $ndkTarget 'toolchains\llvm\prebuilt\windows-x86_64\bin\aarch64-linux-android24-clang.exe'
}
Write-Host "NDK готов: $ndkTarget"
Write-Host "aarch64 clang: $(Test-Path $clang) -> $clang"

# --- Платформа android-34 ---
$platTarget = Join-Path $root 'platforms\android-34'
if (-not (Test-Path (Join-Path $platTarget 'android.jar'))) {
    $platZip = Join-Path $dl 'platform-34-ext7_r03.zip'
    Get-LargeFile -Url 'https://dl.google.com/android/repository/platform-34-ext7_r03.zip' -Path $platZip -MinMB 40
    Write-Host 'Распаковка платформы android-34...'
    $stage = Join-Path $dl 'plat-stage'
    if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $stage | Out-Null
    & tar.exe -xf $platZip -C $stage
    if ($LASTEXITCODE -ne 0) { throw 'распаковка платформы не удалась' }
    $inner = Join-Path $stage 'android-14'
    if (-not (Test-Path $inner)) {
        $inner = (Get-ChildItem $stage -Directory | Select-Object -First 1).FullName
    }
    if (Test-Path $platTarget) { Remove-Item $platTarget -Recurse -Force }
    New-Item -ItemType Directory -Force -Path (Split-Path $platTarget -Parent) | Out-Null
    Move-Item $inner $platTarget
    Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue
}
Write-Host ("android.jar: {0}" -f (Test-Path (Join-Path $platTarget 'android.jar')))

Write-Host ''
Write-Host '=== итог ==='
foreach ($d in 'ndk','cmake','build-tools','platforms') {
    $p = Join-Path $root $d
    if (Test-Path $p) { Write-Host ("{0}: {1}" -f $d, ((Get-ChildItem $p -Directory | Select-Object -Expand Name) -join ', ')) }
    else { Write-Host ("{0}: missing" -f $d) }
}
