# Сборка HumanGram Android с повторами.
#
# Сеть в этой среде нестабильна: соединения с репозиториями периодически
# обрываются на этапе TLS-рукопожатия
# ("Remote host terminated the handshake"). Gradle кэтирует всё, что
# успело загрузиться, поэтому повторные запуски постепенно продвигают
# сборку: неудачные попытки не теряют уже скачанное.
param(
    [Parameter(Mandatory)][string]$ApiId,
    [Parameter(Mandatory)][string]$ApiHash,
    [ValidateSet('Release', 'Standalone', 'Debug')]
    [string]$Variant = 'Release',
    [switch]$Universal,
    [int]$MaxAttempts = 15
)

$ErrorActionPreference = 'Stop'
$root = 'G:\HumanGram'
$script = Join-Path $root 'build\build-android.ps1'
$logs = Join-Path $root 'build\logs'
$buildLog = Join-Path $logs 'android-build.log'

for ($i = 1; $i -le $MaxAttempts; $i++) {
    Write-Host ""
    Write-Host "=== Попытка $i из $MaxAttempts ===" -ForegroundColor Cyan

    $arguments = @('-ApiId', $ApiId, '-ApiHash', $ApiHash, '-SkipProxyBuild')
    if ($Variant -ne 'Release') { $arguments += @('-Variant', $Variant) }
    if ($Universal) { $arguments += '-Universal' }

    & pwsh -NoProfile -ExecutionPolicy Bypass -File $script @arguments
    $rc = $LASTEXITCODE

    if ($rc -eq 0) {
        Write-Host 'Сборка Android завершена.' -ForegroundColor Green
        exit 0
    }

    # Показываем причину: часть сбоев сетевая и требует повтора,
    # часть — нет, и тогда повтор не поможет.
    $reason = (Get-Content $buildLog -ErrorAction SilentlyContinue |
        Select-String -Pattern 'handshake|Could not GET|Could not resolve|timed out|Execution failed for task|error:|FAILURE' |
        Select-Object -Last 3 | ForEach-Object { $_.Line.Trim() }) -join ' | '
    if ($reason) { Write-Host "причина: $reason" -ForegroundColor Yellow }

    if (Test-Path $buildLog) {
        if (Select-String -Path $buildLog -Pattern 'BUILD SUCCESSFUL' -Quiet) {
            Write-Host 'Сборка фактически успешна.' -ForegroundColor Green
            exit 0
        }
    }

    Start-Sleep -Seconds 15
}

Write-Host 'Сборка Android не завершена после всех попыток.' -ForegroundColor Red
exit 1
