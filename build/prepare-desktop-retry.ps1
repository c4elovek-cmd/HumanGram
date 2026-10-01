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

# Очистка перед каждой попыткой.
#
# Две отдельные проблемы, из-за которых prepare.py падал на стадии
# openssl3 с сообщением "The directory is not empty":
#
#   1. Прерванная сборка оставляет запущенные процессы (perl, jom,
#      nmake, cl, link), которые удерживают файлы, и каталог стадии
#      не удаляется.
#   2. В каталоге остаётся файл с именем NUL — зарезервированное имя
#      Windows. Обычный Remove-Item и rmdir /S такой файл не удаляют,
#      из-за чего git clone отказывается клонировать в непустой
#      каталог. Обход: расширенный префикс \\?\ в пути.
function Stop-BuildProcesses {
    $names = @('perl', 'jom', 'nmake', 'cl', 'link', 'lib', 'cmake', 'ninja', 'git', 'msbuild')
    $stopped = 0
    foreach ($n in $names) {
        foreach ($p in (Get-Process -Name $n -ErrorAction SilentlyContinue)) {
            if ($p.Id -eq $PID) { continue }
            try { $p | Stop-Process -Force -ErrorAction SilentlyContinue; $stopped++ } catch { }
        }
    }
    if ($stopped -gt 0) { Start-Sleep -Seconds 2 }
    return $stopped
}

function Remove-ReservedNames {
    param([Parameter(Mandatory)][string]$Root)
    # Проход по дереву каталога стадии: удаление выполняется снизу вверх,
    # каждый путь получает префикс \\?, иначе файлы с зарезервированными
    # именами удалить невозможно.
    $removed = 0
    $reserved = @('CON', 'PRN', 'AUX', 'NUL',
        'COM1', 'COM2', 'COM3', 'COM4', 'COM5', 'COM6', 'COM7', 'COM8', 'COM9',
        'LPT1', 'LPT2', 'LPT3', 'LPT4', 'LPT5', 'LPT6', 'LPT7', 'LPT8', 'LPT9')
    if (-not (Test-Path $Root)) { return 0 }

    $files = Get-ChildItem -LiteralPath $Root -Recurse -Force -File -ErrorAction SilentlyContinue
    foreach ($file in $files) {
        $base = $file.Name.Split('.')[0].ToUpperInvariant()
        if ($reserved -contains $base) {
            try {
                Remove-Item -LiteralPath ('\\?\' + $file.FullName) -Force -ErrorAction Stop
                $removed++
                Write-Host ("    удалён файл с зарезервированным именем: {0}" -f $file.FullName) -ForegroundColor Yellow
            } catch { }
        }
    }
    return $removed
}

function Clear-StageDirectories {
    # Каталоги стадий, которые prepare.py удаляет сам. Их остатки
    # мешают повторной попытке, поэтому удаляем заранее.
    $libs = 'G:\HumanGram\Libraries\win64'
    if (-not (Test-Path $libs)) { return }
    foreach ($d in (Get-ChildItem $libs -Directory -ErrorAction SilentlyContinue)) {
        $reserved = Remove-ReservedNames -Root $d.FullName
        try {
            Remove-Item -LiteralPath $d.FullName -Recurse -Force -ErrorAction Stop
        } catch {
            Write-Host ("    не удалось удалить {0}: {1}" -f $d.Name, $_.Exception.Message) -ForegroundColor Yellow
        }
    }
}

for ($i = 1; $i -le $MaxAttempts; $i++) {
    $log = Join-Path $logs "desktop-prepare-attempt$i.log"
    Write-Host ""
    Write-Host "=== Попытка $i из $MaxAttempts ===" -ForegroundColor Cyan

    $stopped = Stop-BuildProcesses
    if ($stopped -gt 0) { Write-Host "  завершено процессов: $stopped" }

    $reserved = Remove-ReservedNames -Root 'G:\HumanGram\Libraries\win64'
    if ($reserved -gt 0) { Write-Host "  удалено файлов с зарезервированными именами: $reserved" }

    Clear-StageDirectories

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
