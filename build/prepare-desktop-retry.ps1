# Подготовка зависимостей Telegram Desktop с повторами.
#
# Сеть в этой среде нестабильна: клонирование репозиториев и загрузка
# архивов периодически завершаются ошибками TLS. prepare.py при этом
# корректно продолжает с места сбоя: готовые стадии помечаются как
# SKIPPING, и повторный запуск их не повторяет. Поэтому достаточно
# повторять запуск, пока не будет достигнута последняя стадия.
param(
    [int]$MaxAttempts = 12
)

$ErrorActionPreference = 'Stop'
$root = 'G:\HumanGram\desktop'
$logs = 'G:\HumanGram\build\logs'
New-Item -ItemType Directory -Force -Path $logs | Out-Null
$vcvars = 'C:\Program Files (x86)\Microsoft Visual Studio\18\BuildTools\VC\Auxiliary\Build\vcvars64.bat'

for ($i = 1; $i -le $MaxAttempts; $i++) {
    $log = Join-Path $logs "desktop-prepare-attempt$i.log"
    Write-Host ""
    Write-Host "=== Попытка $i из $MaxAttempts ===" -ForegroundColor Cyan
    cmd /c "call `"$vcvars`" && cd /d $root\Telegram && build\prepare\win.bat silent > `"$log`" 2>&1"
    $rc = $LASTEXITCODE

    $stages = 0
    if (Test-Path $log) {
        $stages = (Get-Content $log | Select-String -Pattern '^\[\d+/33\]').Count
    }
    Write-Host "код: $rc, стадий выполнено: $stages"

    if ($rc -eq 0) {
        Write-Host 'Подготовка зависимостей завершена.' -ForegroundColor Green
        exit 0
    }

    # Показываем причину сбоя, чтобы было видно, что именно не удалось.
    $reason = (Get-Content $log -ErrorAction SilentlyContinue |
        Select-String -Pattern 'error|Error|FAILED|not found|schannel|fatal:' |
        Select-Object -Last 3 | ForEach-Object { $_.Line.Trim() }) -join ' | '
    if ($reason) { Write-Host "причина: $reason" -ForegroundColor Yellow }

    if ($stages -ge 33) {
        Write-Host 'Все стадии выполнены, несмотря на ненулевой код.' -ForegroundColor Green
        exit 0
    }
    Start-Sleep -Seconds 10
}

Write-Host 'Подготовка зависимостей не завершена после всех попыток.' -ForegroundColor Red
exit 1
