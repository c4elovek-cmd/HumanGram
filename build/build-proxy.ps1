<#
.SYNOPSIS
    Сборка HumanGram-Proxy: библиотека для Windows (.dll), библиотека для
    Android (.so, arm64-v8a) и утилита командной строки (.exe).

.DESCRIPTION
    Один исходный код собирается во все три артефакта. Сборка библиотек
    в режиме c-shared требует C-компилятора:

      * Windows — gcc из MinGW-w64 (winget install BrechtSanders.WinLibs.POSIX.UCRT);
      * Android  — clang из состава Android NDK (см. build/install-android-sdk.ps1).

    Если C-компилятор не найден, сборка библиотек пропускается с явным
    сообщением, а утилита командной строки собирается всегда, поскольку
    не требует cgo.

.PARAMETER AndroidNDK
    Корневая папка Android NDK, например G:\HumanGram\android-sdk\ndk\27.0.12077973.
    Если не задана, предпринимается попытка найти NDK автоматически.

.PARAMETER SkipTests
    Пропустить проверку go vet и go test.

.EXAMPLE
    pwsh -File build\build-proxy.ps1
#>
[CmdletBinding()]
param(
    [string]$AndroidNDK = '',
    [string[]]$AndroidAbis = @('arm64-v8a'),
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$proxy = Join-Path $root 'proxy'
$out = Join-Path $root 'dist\lib'
New-Item -ItemType Directory -Force -Path $out | Out-Null

# Список архитектур можно передать и через запятую одним аргументом,
# и отдельными аргументами — приводим к единому виду.
$AndroidAbis = @(
    foreach ($item in $AndroidAbis) {
        $item -split ',' | ForEach-Object { $_.Trim() } | Where-Object { $_ }
    }
)

# Проверка наличия JNI-символов в собранной библиотеке Android.
#
# Зачем: Go в режиме c-shared экспортирует обычные C-символы, а Java
# связывается с нативными методами по схеме JNI. Символы вида
# Java_org_telegram_messenger_HumanGramProxy_nativeStart появляются
# только если bridge/jni_android.go попал в сборку (он ограничен
# тегом android). Если по какой-то причине файл не включится —
# например, из-за неверного тега или новой платформы, — библиотека
# соберётся, APK соберётся, приложение запустится и тихо не будет
# работать: в журнале появится "No implementation found for ...".
#
# Такой отказ уже случался: библиотека была собрана без JNI-моста,
# и ошибка обнаружилась только при запуске на телефоне. Поэтому
# проверка встроена в сборку и останавливает её сразу.
function Assert-JniSymbols {
    param([Parameter(Mandatory)][string]$Path)

    $expected = @(
        'nativeStart', 'nativeAddress', 'nativeLastError',
        'nativeFreeString', 'nativeStop', 'nativeIsRunning',
        'nativeSessions', 'nativeVersion'
    )
    $bytes = [System.IO.File]::ReadAllBytes($Path)
    $text = [System.Text.Encoding]::ASCII.GetString($bytes)

    $missing = @(
        foreach ($name in $expected) {
            if ($text -notmatch [regex]::Escape("HumanGramProxy_$name")) { $name }
        }
    )
    if ($missing.Count -gt 0) {
        throw ("В {0} нет JNI-символов: {1}. Библиотека собрана без " +
            "bridge\jni_android.go — приложение запустится, но прокси " +
            "не поднимется.") -f (Split-Path $Path -Leaf), ($missing -join ', ')
    }
}

function Write-Step($msg) {
    Write-Host ''
    Write-Host "==> $msg" -ForegroundColor Cyan
}

Push-Location $proxy
try {
    Write-Step 'Проверка исходников'
    go version
    if (-not $SkipTests) {
        go vet ./...
        if ($LASTEXITCODE -ne 0) { throw 'go vet завершился с ошибкой' }
        go test ./... -count=1
        if ($LASTEXITCODE -ne 0) { throw 'go test завершился с ошибкой' }
    }

    # --- Утилита командной строки: собирается всегда, cgo не требуется ---
    Write-Step 'Сборка утилиты командной строки (windows/amd64)'
    $env:CGO_ENABLED = '0'
    go build -trimpath -o (Join-Path $out 'humangram-proxy.exe') ./cmd/humangram-proxy
    if ($LASTEXITCODE -ne 0) { throw 'не удалось собрать утилиту командной строки' }

    # --- Поиск C-компилятора для библиотек ---
    $cc = $null
    if ($env:CC) { $cc = $env:CC }

    $ndkRoot = $AndroidNDK
    if (-not $ndkRoot) {
        $sdk = 'G:\HumanGram\android-sdk\ndk'
        if (Test-Path $sdk) {
            $latest = Get-ChildItem $sdk -Directory | Sort-Object Name -Descending | Select-Object -First 1
            if ($latest) { $ndkRoot = $latest.FullName }
        }
    }

    $androidCC = $null
    if ($ndkRoot) {
        $toolchain = Join-Path $ndkRoot 'toolchains\llvm\prebuilt\windows-x86_64\bin'
        $candidate = Join-Path $toolchain 'aarch64-linux-android24-clang.cmd'
        if (Test-Path $candidate) { $androidCC = $candidate }
        else {
            $plain = Join-Path $toolchain 'aarch64-linux-android24-clang.exe'
            if (Test-Path $plain) { $androidCC = $plain }
        }
    }

    if (-not $cc) {
        $gcc = Get-Command gcc -ErrorAction SilentlyContinue
        if ($gcc) { $cc = $gcc.Source }
    }
    if (-not $cc) {
        $winGet = Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Packages'
        if (Test-Path $winGet) {
            $found = Get-ChildItem $winGet -Recurse -Filter 'gcc.exe' -ErrorAction SilentlyContinue |
                Where-Object { $_.FullName -like '*mingw*' } | Select-Object -First 1
            if ($found) { $cc = $found.FullName }
        }
    }

    # --- Библиотека для Windows ---
    if ($cc) {
        Write-Step "Сборка HumanGramProxy.dll (windows/amd64, c-shared)"
        Write-Host "    C-компилятор: $cc"
        $env:CC = $cc
        $env:CGO_ENABLED = '1'
        $dll = Join-Path $out 'HumanGramProxy.dll'
        go build -trimpath -buildmode=c-shared -o $dll ./bridge
        if ($LASTEXITCODE -ne 0) { throw 'не удалось собрать HumanGramProxy.dll' }
    } else {
        Write-Warning 'C-компилятор не найден: HumanGramProxy.dll не собран.'
        Write-Warning 'Установите MinGW-w64: winget install BrechtSanders.WinLibs.POSIX.UCRT'
    }

    # --- Библиотека для Android ---
    # Собираются архитектуры, перечисленные в -AndroidAbis. По умолчанию
    # только arm64-v8a: это сокращает время сборки вчетверо. Для
    # универсального APK перечисляются все четыре ABI.
    if ($AndroidAbis -and $androidCC) {
        Write-Step "Сборка libhumangramproxy.so для $($AndroidAbis -join ', ')"
        Write-Host "    C-компилятор: $androidCC"
        $env:CC = $androidCC
        $env:CGO_ENABLED = '1'
        $env:GOOS = 'android'
        $failed = @()
        foreach ($abi in $AndroidAbis) {
            $env:GOARCH = switch ($abi) {
                'arm64-v8a' { 'arm64' }
                'armeabi-v7a' { 'arm' }
                'x86_64' { 'amd64' }
                'x86' { '386' }
                default { $null }
            }
            if (-not $env:GOARCH) {
                Write-Warning "Неизвестная архитектура $abi, пропускается"
                continue
            }
            # Android API: 24 для 64-разрядных, 21 для 32-разрядных.
            $api = if ($env:GOARCH -eq 'arm64' -or $env:GOARCH -eq 'amd64') { 24 } else { 21 }
            $cc = "aarch64-linux-android$api-clang"
            if ($env:GOARCH -eq 'arm') { $cc = "armv7a-linux-androideabi$api-clang" }
            if ($env:GOARCH -eq 'amd64') { $cc = "x86_64-linux-android$api-clang" }
            if ($env:GOARCH -eq '386') { $cc = "i686-linux-android$api-clang" }
            $env:CC = Join-Path (Split-Path $androidCC -Parent) "$cc.cmd"

            $abiDir = Join-Path $out "android\$abi"
            New-Item -ItemType Directory -Force -Path $abiDir | Out-Null
            $so = Join-Path $abiDir 'libhumangramproxy.so'
            Write-Host "    -> $abi"
            go build -trimpath -buildmode=c-shared -o $so ./bridge
            if ($LASTEXITCODE -ne 0) {
                $failed += $abi
                Write-Warning "сборка для $abi не удалась"
            }
        }
        Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
        if ($failed.Count -gt 0) {
            throw "не удалось собрать .so для: $($failed -join ', ')"
        }
        Write-Host 'Копирование библиотек в android/TMessagesProj/jni/'
        $jniRoot = Join-Path (Split-Path $PSScriptRoot -Parent) 'android\TMessagesProj\jni'
        foreach ($abi in $AndroidAbis) {
            $src = Join-Path $out "android\$abi\libhumangramproxy.so"
            if (Test-Path $src) {
                Assert-JniSymbols -Path $src
                $dst = Join-Path $jniRoot "$abi\libhumangramproxy.so"
                New-Item -ItemType Directory -Force -Path (Split-Path $dst -Parent) | Out-Null
                Copy-Item $src $dst -Force
                Write-Host ("    {0} -> {1}" -f $abi, $dst)
            }
        }
    } elseif (-not $androidCC) {
        Write-Warning 'Android NDK не найден: libhumangramproxy.so не собран.'
        Write-Warning 'Установите NDK: pwsh -File build\install-android-pkgs.ps1'
    }
}
finally {
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    Pop-Location
}

Write-Step 'Готовые артефакты'
Get-ChildItem $out -ErrorAction SilentlyContinue |
    Select-Object Name, @{Name='Size';Expression={'{0:N1} МБ' -f ($_.Length/1MB)}} |
    Format-Table -AutoSize
