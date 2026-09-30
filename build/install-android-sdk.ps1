# Установка Android SDK/NDK/CMake в G:\HumanGram\android-sdk
# Диск C не используется: все пакеты ставятся в SDK-root на диске G.
$ErrorActionPreference = 'Stop'
$root = 'G:\HumanGram\android-sdk'
$dl   = 'G:\HumanGram\.cache\dl'
New-Item -ItemType Directory -Force -Path $root, $dl | Out-Null

$cmdlineZip = Join-Path $dl 'commandlinetools-win.zip'
if (-not (Test-Path $cmdlineZip)) {
  $url = 'https://dl.google.com/android/repository/commandlinetools-win-11076708_latest.zip'
  Write-Output "Скачивание $url"
  Invoke-WebRequest -Uri $url -OutFile $cmdlineZip -UseBasicParsing
}
Write-Output ("cmdline-tools: {0:N1} МБ" -f ((Get-Item $cmdlineZip).Length/1MB))

$extract = Join-Path $root 'cmdline-tools'
if (-not (Test-Path (Join-Path $extract 'latest\bin\sdkmanager.bat'))) {
  $tmp = Join-Path $dl 'cmdline-tmp'
  if (Test-Path $tmp) { Remove-Item $tmp -Recurse -Force }
  New-Item -ItemType Directory -Force -Path $tmp | Out-Null
  Expand-Archive -Path $cmdlineZip -DestinationPath $tmp -Force
  New-Item -ItemType Directory -Force -Path $extract | Out-Null
  if (Test-Path (Join-Path $extract 'latest')) { Remove-Item (Join-Path $extract 'latest') -Recurse -Force }
  Move-Item (Join-Path $tmp 'cmdline-tools') (Join-Path $extract 'latest')
  Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
$sdkmanager = Join-Path $extract 'latest\bin\sdkmanager.bat'
Write-Output "sdkmanager: $sdkmanager"

$env:JAVA_HOME = 'G:\Program Files\Java\Java17Temurin'

# Принимаем лицензии: сначала записываем файлы отпечатков, затем
# подтверждаем интерактивные запросы потоком ответов.
$licenseDir = Join-Path $root 'licenses'
New-Item -ItemType Directory -Force -Path $licenseDir | Out-Null

$licenses = @{
  'android-sdk-license' = @(
    '8933bad161af4178b1185d1a37fbf41ea5269c55',
    'd56f5187479451eabf01fb78af6dfcb131a6481e',
    '24333f8a63b6825ea9c5514f83c2829b004d1fee')
  'android-sdk-preview-license' = @('84831b9409646a918e30573bab4c9c91346d8abd')
  'android-sdk-arm-dbt-license' = @('859f317696f67ef3d7f30a50a5560e7834b43903')
  'google-gdk-license' = @('33b6a2b64607f11b759f320ef9dff4ae5c47d97a')
  'intel-android-extra-license' = @('d975f751698a77b662f1254ddbeed3901e976f5a')
  'mips-android-sysimage-license' = @('e9acab5b5bfbb5a768319957aabdd8e2cc97d1a8')
}
foreach ($name in $licenses.Keys) {
  Set-Content -Path (Join-Path $licenseDir $name) -Value ($licenses[$name] -join "`n") -Encoding ascii
}
Write-Output "Записаны файлы лицензий: $($licenses.Keys -join ', ')"

# Подтверждаем оставшиеся интерактивные запросы.
$answers = (1..40 | ForEach-Object { 'y' }) -join "`n"
$answers | & $sdkmanager --sdk_root=$root --licenses 2>&1 | Select-Object -Last 5

$packages = @(
  'platform-tools',
  'platforms;android-34',
  'build-tools;34.0.0',
  'ndk;27.0.12077973',
  'cmake;3.22.1'
)

Write-Output 'Установка пакетов SDK/NDK (может занять 10-25 минут)...'
$answers | & $sdkmanager --sdk_root=$root @packages 2>&1 | Select-Object -Last 20

Write-Output '=== установленные компоненты ==='
foreach ($d in 'ndk','cmake','build-tools','platforms') {
  $p = Join-Path $root $d
  if (Test-Path $p) { Write-Output ("{0}: {1}" -f $d, ((Get-ChildItem $p -Directory | Select-Object -Expand Name) -join ', ')) }
  else { Write-Output ("{0}: отсутствует" -f $d) }
}
