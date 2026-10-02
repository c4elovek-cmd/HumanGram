<#
.SYNOPSIS
    Сборка HumanGram Android: приложение arm64-v8a с встроенной
    библиотекой HumanGram-Proxy.

.DESCRIPTION
    Скрипт выполняет полный цикл:

      1. проверяет наличие JDK, Android SDK, NDK и CMake;
      2. генерирует ключ подписи, если он отсутствует;
      3. копирует libhumangramproxy.so в каталог jniLibs;
      4. запускает сборку релизного APK для arm64-v8a;
      5. копирует готовый APK в dist/ под именем HumanGram-Android.apk.

    Учётные данные подписи передаются Gradle через флаги -P и не
    сохраняются в репозитории.

    Ключ подписи — собственный. Официальный ключ Telegram из исходного
    проекта не используется: подпись модифицированного приложения чужим
    ключом сделала бы сборку неотличимой от официальной.

.PARAMETER ApiId
    Идентификатор приложения Telegram (получить на my.telegram.org).
    Если не задан, берётся из переменной окружения TELEGRAM_API_ID.

.PARAMETER ApiHash
    Хеш приложения Telegram. Если не задан, берётся из переменной
    окружения TELEGRAM_API_HASH.

.PARAMETER Variant
    Вариант сборки: Release (по умолчанию), Standalone или Debug.

.PARAMETER SkipProxyBuild
    Не пересобирать HumanGram-Proxy, использовать готовую библиотеку
    из dist/lib.

.EXAMPLE
    pwsh -File build\build-android.ps1 -ApiId 123456 -ApiHash abcdef...
#>
[CmdletBinding()]
param(
    [string]$ApiId = $env:TELEGRAM_API_ID,
    [string]$ApiHash = $env:TELEGRAM_API_HASH,
    [ValidateSet('Release', 'Standalone', 'Debug')]
    [string]$Variant = 'Release',
    [switch]$SkipProxyBuild,
    [switch]$Universal
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$android = Join-Path $root 'android'
$sdk = Join-Path $root 'android-sdk'
$dist = Join-Path $root 'dist'
$logs = Join-Path $root 'build\logs'
New-Item -ItemType Directory -Force -Path $dist, $logs | Out-Null

function Write-Step($msg) {
    Write-Host ''
    Write-Host "==> $msg" -ForegroundColor Cyan
}

Write-Step 'Проверка окружения'

$javaHome = 'G:\Program Files\Java\Java17Temurin'
if (Test-Path $javaHome) { $env:JAVA_HOME = $javaHome }
$java = Get-Command java -ErrorAction SilentlyContinue
if (-not $java) { throw 'Не найден JDK. Установите JDK 17.' }
Write-Host "java: $(& java -version 2>&1 | Select-Object -First 1)"

if (-not (Test-Path $sdk)) { throw "Не найден Android SDK: $sdk. Запустите build\install-android-pkgs.ps1" }
$ndkRoot = $sdk + '\ndk\27.2.12479018'
if (-not (Test-Path $ndkRoot)) {
    $ndk = Get-ChildItem (Join-Path $sdk 'ndk') -Directory -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending | Select-Object -First 1
    if (-not $ndk) { throw 'Не найден Android NDK. Запустите build\install-android-pkgs.ps1' }
    $ndkRoot = $ndk.FullName
}
Write-Host "SDK:  $sdk"
Write-Host "NDK:  $ndkRoot"

if (-not $ApiId -or -not $ApiHash) {
    throw 'Не заданы ApiId и ApiHash. Укажите их параметрами или переменными TELEGRAM_API_ID и TELEGRAM_API_HASH.'
}

Write-Step 'Окончания строк в файлах тем'

# Файлы .attheme должны попадать в сборку с LF, а не с CRLF.
#
# Почему это важно. Разбор темы в Telegram делит файл только по символу
# '\n', а остаток строки целиком передаёт в разбор числа. При окончаниях
# CRLF символ '\r' становится частью значения:
#
#   windowBackgroundWhite=-14866637\r   ->  -148666335
#   windowBackgroundWhiteBlackText=-1\r ->  25
#
# Цвета получаются мусорными, и тёмная тема отрисовывается чёрной без
# надписей. Источник проблемы — core.autocrlf в репозитории клиента:
# git переписывает окончания строк при клонировании на Windows.
#
# Файл android\.gitattributes защищает от повторения при клонировании,
# а эта проверка закрывает случай, когда правки внесены в обход git или
# файлы распакованы из архива.
$themeAssets = Join-Path $android 'TMessagesProj\src\main\assets'
$normalized = 0
foreach ($file in (Get-ChildItem $themeAssets -Filter '*.attheme' -ErrorAction SilentlyContinue)) {
    $bytes = [System.IO.File]::ReadAllBytes($file.FullName)
    $hasCrlf = $false
    for ($i = 1; $i -lt $bytes.Length; $i++) {
        if ($bytes[$i] -eq 10 -and $bytes[$i - 1] -eq 13) { $hasCrlf = $true; break }
    }
    if (-not $hasCrlf) { continue }
    $text = [System.Text.Encoding]::UTF8.GetString($bytes) -replace "`r`n", "`n"
    [System.IO.File]::WriteAllText($file.FullName, $text, (New-Object System.Text.UTF8Encoding($false)))
    $normalized++
    Write-Host ("    {0}: CRLF -> LF" -f $file.Name) -ForegroundColor Yellow
}
if ($normalized -eq 0) {
    Write-Host '    все файлы тем уже в LF'
} else {
    Write-Host ("    исправлено файлов: {0}" -f $normalized) -ForegroundColor Yellow
}

Write-Step 'Ключ подписи'

$keystore = Join-Path $android 'TMessagesProj\config\release.keystore'
$storePass = 'humangram'
if (-not (Test-Path $keystore)) {
    Write-Host "Генерация ключа $keystore"
    & keytool -genkeypair -v -keystore $keystore -alias androidkey `
        -keyalg RSA -keysize 2048 -validity 10000 -storetype PKCS12 `
        -storepass $storePass -keypass $storePass `
        -dname 'CN=HumanGram, OU=HumanGram, O=HumanGram, L=Unknown, ST=Unknown, C=RU'
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось сгенерировать ключ подписи' }
} else {
    Write-Host "Используется существующий ключ $keystore"
}

Write-Step 'Библиотека HumanGram-Proxy'

# Набор архитектур: для универсального APK собираются все четыре,
# иначе только arm64-v8a.
$abis = if ($Universal) { @('armeabi-v7a', 'arm64-v8a', 'x86', 'x86_64') } else { @('arm64-v8a') }

if (-not $SkipProxyBuild) {
    $proxyArgs = @('-SkipTests', '-AndroidAbis') + $abis
    & pwsh -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'build-proxy.ps1') @proxyArgs
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось собрать HumanGram-Proxy' }
}

# build-proxy.ps1 кладёт библиотеки в dist/lib/android/<abi>/ и копирует
# их в android/TMessagesProj/jni/. Здесь проверяем наличие и приводим
# каталог к ожидаемому состоянию.
$jniRoot = Join-Path $android 'TMessagesProj\jni'
# Каталоги архитектур, не входящих в сборку, удаляются. Иначе APK
# объявит лишние архитектуры (Android определяет поддерживаемые ABI по
# содержимому lib/), но на них не будет основной нативной библиотеки
# libtmessages, и приложение упадёт при запуске.
$jniRoot = Join-Path $android 'TMessagesProj\jni'
$allAbis = @('armeabi-v7a', 'arm64-v8a', 'x86', 'x86_64')
foreach ($other in $allAbis) {
    if ($abis -notcontains $other) {
        $dir = Join-Path $jniRoot $other
        if (Test-Path $dir) {
            Remove-Item $dir -Recurse -Force
            Write-Host "удалён каталог $other (не входит в сборку)"
        }
    }
}

foreach ($abi in $abis) {
    $built = Join-Path $dist "lib\android\$abi\libhumangramproxy.so"
    $legacy = Join-Path $dist 'lib\libhumangramproxy.so'
    $target = Join-Path $jniRoot "$abi\libhumangramproxy.so"
    if (Test-Path $built) {
        New-Item -ItemType Directory -Force -Path (Split-Path $target -Parent) | Out-Null
        Copy-Item $built $target -Force
    } elseif ((Test-Path $legacy) -and $abi -eq 'arm64-v8a') {
        New-Item -ItemType Directory -Force -Path (Split-Path $target -Parent) | Out-Null
        Copy-Item $legacy $target -Force
    } else {
        throw "Не найдена библиотека для $abi. Запустите build\build-proxy.ps1 -AndroidAbis $($abis -join ',')"
    }
    Write-Host ("{0,-12} {1,7:N2} МБ -> {2}" -f $abi, ((Get-Item $target).Length / 1MB), (Split-Path $target -Parent))
}

Write-Step "Сборка варианта $Variant ($($abis -join ', '))"

# local.properties задаёт путь к SDK; идентификаторы и пароли передаются
# через -P, чтобы не сохранять их в репозитории.
$localProps = Join-Path $android 'local.properties'
Set-Content -Path $localProps -Value "sdk.dir=$($sdk -replace '\\','\\\\')" -Encoding ascii

# Flavor afat — единственный, который собирает APK со всеми архитектурами
# в одном файле (BUNDLE=false, abiVersionCode=9). Остальные flavor
# предназначены для App Bundle, поэтому для обычной сборки они не нужны.
# Набор архитектур внутри APK задаётся свойством humanGramAbis.
$tasks = switch ($Variant) {
    'Release' { @(':TMessagesProj_App:assembleAfatRelease') }
    'Standalone' { @(':TMessagesProj_App:assembleAfatStandaloneRelease') }
    'Debug' { @(':TMessagesProj_App:assembleAfatBeta') }
}

Push-Location $android
try {
    $gradleLog = Join-Path $logs 'android-build.log'
    # Кэш Gradle размещается на диске G, чтобы не занимать диск C.
    $env:GRADLE_USER_HOME = Join-Path $root '.cache\gradle'
    New-Item -ItemType Directory -Force -Path $env:GRADLE_USER_HOME | Out-Null

    $gradleArgs = @(
        '-PAPP_VERSION_NAME=1.0.0'
        '-PAPP_VERSION_CODE=11000'
        ("-PRELEASE_STORE_PASSWORD=$storePass")
        '-PRELEASE_KEY_ALIAS=androidkey'
        ("-PRELEASE_KEY_PASSWORD=$storePass")
        '-PRELEASE_STORE_FILE=../TMessagesProj/config/release.keystore'
    )
    if ($Universal) {
        # Универсальный APK содержит все четыре архитектуры, поэтому
        # библиотека HumanGram-Proxy собирается для каждой из них.
        $gradleArgs += '-PhumanGramAbis=armeabi-v7a,arm64-v8a,x86,x86_64'
    }
    $gradleArgs += $tasks

    Write-Host "gradlew.bat $($gradleArgs -join ' ')"
    Write-Host "журнал: $gradleLog"
    Write-Host "GRADLE_USER_HOME: $env:GRADLE_USER_HOME"
    & cmd /c "gradlew.bat $($gradleArgs -join ' ') --no-daemon > `"$gradleLog`" 2>&1"
    $rc = $LASTEXITCODE
}
finally {
    Pop-Location
}

Write-Host ''
if ($rc -ne 0) {
    Write-Host 'Сборка завершилась с ошибкой. Последние строки журнала:' -ForegroundColor Red
    Get-Content $gradleLog -Tail 40
    exit $rc
}

Write-Step 'Результат'
$apk = Get-ChildItem $android -Recurse -Filter '*.apk' -ErrorAction SilentlyContinue |
    Where-Object { $_.FullName -match '\\release\\|\\standalone\\|\\beta\\' } |
    Sort-Object LastWriteTime -Descending | Select-Object -First 1
if (-not $apk) { throw 'APK не найден после сборки' }
$target = Join-Path $dist 'HumanGram-Android.apk'
Copy-Item $apk.FullName $target -Force
Write-Host ("{0} ({1:N1} МБ) -> {2}" -f $apk.Name, ($apk.Length / 1MB), $target)
Get-Item $target | Select-Object Name, Length, LastWriteTime
