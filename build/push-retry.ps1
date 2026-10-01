# Отправка изменений в GitHub с повторами.
#
# Сеть в этой среде нестабильна: соединения с github.com регулярно
# обрываются. Коммиты при этом сохранены локально и не теряются —
# скрипт лишь повторяет попытки отправки, пока сеть недоступна.
param(
    [int]$MaxAttempts = 60,
    [int]$DelaySeconds = 45,
    [string]$Branch = 'main'
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# Отправляем только если есть что отправлять.
$status = git status --porcelain
$local = git rev-parse HEAD
$remote = ''
try { $remote = (git rev-parse "origin/$Branch" 2>$null) } catch { $remote = '' }

if ($local -eq $remote) {
    Write-Host "Все изменения уже отправлены ($Branch = $($local.Substring(0,7)))."
    exit 0
}

Write-Host "Локальный коммит: $($local.Substring(0,7))"
Write-Host "На сервере:       $(if($remote){$remote.Substring(0,7)}else{'нет ветки'})"
Write-Host "Отправка с повторами, до $MaxAttempts попыток." -ForegroundColor Cyan

for ($i = 1; $i -le $MaxAttempts; $i++) {
    $output = git push origin $Branch 2>&1
    if ($LASTEXITCODE -eq 0) {
        Write-Host "Отправлено с попытки $i." -ForegroundColor Green
        exit 0
    }
    $why = ($output | Select-Object -First 1)
    Write-Host ("[{0:HH:mm:ss}] попытка {1}: {2}" -f (Get-Date), $i, $why)
    Start-Sleep -Seconds $DelaySeconds
}

Write-Host 'Отправка не удалась. Изменения сохранены локально: повторите позже.' -ForegroundColor Yellow
exit 1