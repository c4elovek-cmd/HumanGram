# HumanGram-Proxy

Локальный прокси MTProto-over-WebSocket, встраиваемый в клиенты HumanGram.

Модуль поднимает прокси на `127.0.0.1` со свободным портом и переносит
трафик клиента Telegram к серверам Telegram, при необходимости заворачивая
его в WebSocket. Клиенту не нужно устанавливать сторонние программы и
настраивать прокси вручную: адрес и порт сообщаются приложением при старте.

Один и тот же исходный код собирается в три артефакта:

| Артефакт | Платформа | Назначение |
|---|---|---|
| `HumanGramProxy.dll` | Windows x64 | встраивается в HumanGram Desktop |
| `libhumangramproxy.so` | Android arm64-v8a | встраивается в HumanGram Android |
| `humangram-proxy.exe` | Windows x64 | запуск из командной строки, диагностика |

## Схема работы

```
Клиент Telegram              HumanGram-Proxy                 Сервер Telegram
       |                            |                                 |
       | TCP + обфускация MTProto   |                                 |
       |--------------------------->|                                 |
       |     64-байтовый префикс    |                                 |
       |<---------------------------|                                 |
       |  кадры MTProto (abridged,  | TCP + обфускация MTProto       |
       |  secure или intermediate)  | либо WebSocket (wss)            |
       |                            |-------------------------------->|
       |                            |     кадры MTProto               |
       |                            |<--------------------------------|
```

Прокси работает прозрачно. Ключ авторизации (`auth_key`) устанавливает сам
клиент в ходе рукопожатия, и прокси в него не вмешивается: ключ никогда не
покидает память клиента, поэтому прокси технически не может прочитать
содержимое переписки. Прокси снимает обфускацию и кадрирование на стороне
клиента и заново применяет их на стороне сервера, формируя собственные
размеры кадров и выравнивание.

Идентификатор дата-центра клиент передаёт в начальном префиксе, поэтому
прокси не требует дополнительной настройки адресов: нужный дата-центр
выбирается автоматически.

## Поддерживаемые протоколы

Обфускация соответствует `TcpConnection::Protocol` из tdesktop, а кадрирование
поддерживается во всех трёх вариантах, объявляемых тегом в префиксе:

| Тег | Протокол | Примечание |
|---|---|---|
| `0xEFEFEFEF` | abridged | длина в 32-битных словах, один или четыре байта заголовка |
| `0xEEEEEEEE` | secure | зарезервированный тег, кадрирование как у abridged |
| `0xDDDDDDDD` | intermediate | длина в байтах, кадр дополняется случайным выравниванием |

## Использование из Go

```go
p, err := humangram.Start(ctx, humangram.Config{
    Secret:   "00112233445566778899aabbccddeeff", // те же 16 байт у клиента
    Upstream: humangram.UpstreamWebSocket,
    WebSocketURL: "wss://example.org/mtproto",
    Logger:   logger,
})
if err != nil {
    return err
}
defer p.Stop()

fmt.Println("прокси слушает", p.Addr()) // например 127.0.0.1:53412
```

## Использование из клиента (C API)

```c
#include "HumanGramProxy.h"

const char *cfg = "{\"secret\":\"00112233445566778899aabbccddeeff\",\"port\":0}";
if (HumanGramProxyStart(cfg) != 0) {
    fprintf(stderr, "%s\n", HumanGramProxyLastError());
    return 1;
}
const char *addr = HumanGramProxyAddr();  // освобождается HumanGramProxyFreeString
int port = HumanGramProxyPort();
...
HumanGramProxyStop();
```

| Функция | Назначение |
|---|---|
| `HumanGramProxyStart(const char*)` | запуск по конфигурации в JSON; 0 — успех |
| `HumanGramProxyAddr()` | адрес `host:port` |
| `HumanGramProxyPort()` | номер порта |
| `HumanGramProxySessions()` | число активных сессий |
| `HumanGramProxyLastError()` | описание последней ошибки |
| `HumanGramProxyFreeString()` | освобождение строки |
| `HumanGramProxyStop()` | остановка |
| `HumanGramProxyIsRunning()` | признак запуска |
| `HumanGramProxyVersion()` | версия модуля |

Коды возврата `HumanGramProxyStart`: `0` — успех, `2` — прокси уже запущен,
`3` — не передана конфигурация, `4` — конфигурация повреждена,
`5` — не удалось запустить прокси.

## Вызов из Java (Android)

Android-клиент не может обратиться к C API напрямую: Java связывается с
нативными методами по схеме JNI и ищет символы с именами вида
`Java_<пакет>_<класс>_<метод>`. Перечисленные выше функции носят обычные
C-имена, поэтому JVM их не увидит.

`bridge/jni_android.go` добавляет недостающий слой: функции
`Java_org_telegram_messenger_HumanGramProxy_native*` переадресуют вызовы
в тот же C API. Файл собирается только под Android (тег `android`), так что
Windows-версия не меняется и продолжает использовать C-символы напрямую.

Соответствие методов:

| Метод в Java | Символ | Вызывает |
|---|---|---|
| `nativeStart(String)` | `..._nativeStart` | `HumanGramProxyStart` |
| `nativeAddress()` | `..._nativeAddress` | `HumanGramProxyAddr` |
| `nativeLastError()` | `..._nativeLastError` | `HumanGramProxyLastError` |
| `nativeFreeString(String)` | `..._nativeFreeString` | ничего: строку освобождает JVM |
| `nativeStop()` | `..._nativeStop` | `HumanGramProxyStop` |
| `nativeIsRunning()` | `..._nativeIsRunning` | `HumanGramProxyIsRunning` |
| `nativeSessions()` | `..._nativeSessions` | `HumanGramProxySessions` |
| `nativeVersion()` | `..._nativeVersion` | `HumanGramProxyVersion` |
| — | `..._nativePort` | `HumanGramProxyPort` (в Java не используется) |

Строки возвращаются как `jstring`, поэтому `nativeFreeString` на стороне
Java — заглушка: освобождать память вручную не нужно.

> При сборке без этого файла библиотека собирается, APK собирается,
> приложение запускается — и не работает. В журнале появляется
> `No implementation found for ...`. Чтобы такое не прошло незамеченным,
> `build\build-proxy.ps1` проверяет наличие всех JNI-символов в готовой
> библиотеке и останавливает сборку с внятной ошибкой.

## Как выбирается адрес WebSocket

Адрес вводить **не нужно**. Номер дата-центра извлекается из
обфускационного init-пакета клиента, и прокси открывает WebSocket к
домену Telegram соответствующего центра:

| Дата-центр | Домен |
|---|---|
| 1 | `zeta.web.telegram.org` |
| 2 | `pluto.web.telegram.org` |
| 3 | `venus.web.telegram.org` |
| 4 | `aurora.web.telegram.org` |
| 5 | `flora.web.telegram.org` |

Путь эндпоинта — `/apiwss`.

Так же работает Flowseal/tg-ws-proxy, у которого сказано: «для работы не
нужны сторонние серверы». Соединение идёт напрямую к инфраструктуре
Telegram, только по домену, а не по адресу.

**Почему это важно.** Прямое TCP-соединение с адресами дата-центров
(например, `149.154.167.51:443`) во многих сетях недоступно: блокировка
строится по адресам. Соединение к тому же центру по домену Telegram для
фильтра выглядит обычным TLS-обменом и проходит. Поэтому режим
`websocket` — режим по умолчанию.

Поле `websocketUrl` остаётся для случая, когда нужен собственный узел:
если оно задано, оно используется для всех центров вместо доменов
Telegram.

Проверить доступность можно так:

```powershell
# Прямое соединение с адресом центра
Test-NetConnection 149.154.167.51 -Port 443
# Соединение по домену того же центра
Test-NetConnection pluto.web.telegram.org -Port 443
```

Если недоступны оба, фильтр блокирует и адреса, и имена: тогда нужен
собственный узел — Cloudflare Worker или Cloudflare-прокси на своём
домене, как описано в документации tg-ws-proxy.

## Ключи конфигурации

| Ключ | Тип | По умолчанию | Описание |
|---|---|---|---|
| `secret` | string | `""` | секрет: 32 шестнадцатеричных символа. Пусто — abridged без шифрования |
| `host` | string | `127.0.0.1` | адрес прослушивания |
| `port` | int | `0` | порт; `0` — свободный |
| `upstream` | string | `websocket` | `websocket` или `direct`. По умолчанию WebSocket: прямое соединение с адресами дата-центров во многих сетях заблокировано |
| `websocketUrl` | string | — | адрес WebSocket-эндпоинта. Пусто — используется домен Telegram того центра, к которому обратился клиент |
| `websocketSubprotocol` | string | `binary` | значение `Sec-WebSocket-Protocol` |
| `insecureSkipVerify` | bool | `false` | не проверять сертификат TLS |
| `dcOverrides` | object | — | адреса дата-центров: `{"2":"host:443,host:443"}` |
| `ipv6Only` | bool | `false` | использовать только адреса IPv6 |
| `handshakeTimeoutMs` | int | `15000` | таймаут установки соединения |
| `idleTimeoutMs` | int | `0` | таймаут простоя сессии; `0` — без ограничения |
| `debug` | bool | `false` | подробный журнал в стандартный поток ошибок |

## Сборка

Требуется Go 1.27 и C-компилятор для сборки библиотек.

```powershell
# Windows: C-компилятор для c-shared
winget install BrechtSanders.WinLibs.POSIX.UCRT

# Android: NDK на диск G
pwsh -File build\install-android-sdk.ps1

# Сборка всех артефактов
pwsh -File build\build-proxy.ps1
```

Сборка отдельных целей вручную:

```powershell
$env:CC = 'путь\к\gcc.exe'
$env:CGO_ENABLED = '1'
go build -trimpath -buildmode=c-shared -o HumanGramProxy.dll ./bridge

$env:CC = 'G:\HumanGram\android-sdk\ndk\27.0.12077973\toolchains\llvm\prebuilt\windows-x86_64\bin\aarch64-linux-android24-clang.cmd'
$env:GOOS = 'android'; $env:GOARCH = 'arm64'
go build -trimpath -buildmode=c-shared -o libhumangramproxy.so ./bridge
```

## Структура кода

```
humangram.go              публичный API: Start, Proxy, Config
bridge/                   C-мост для сборки в виде библиотеки
cmd/humangram-proxy/      утилита командной строки
internal/mtproto/         обфускация (obfuscated2) и кадрирование
internal/transport/       восходящие транспорты: TCP и WebSocket
internal/dc/              справочник адресов дата-центров
internal/session/         ретрансляция трафика между клиентом и сервером
internal/proxy/           прослушивание сокета и жизненный цикл
internal/testutil/        эталонная сторона клиента для тестов
```

## Проверка

```powershell
go vet ./...
go test ./... -count=1
```

Тесты не требуют сети: `internal/mtproto` проверяет совместимость с
эталонной реализацией стороны клиента из tdesktop, а `internal/proxy`
поднимает эмулятор сервера Telegram и прогоняет через прокси реальные
кадры во всех трёх протоколах кадрирования.

## Лицензия

MIT, файл `LICENSE`. Сведения об авторстве исходных работ приведены в
разделе Credits того же файла и в README.md корневого репозитория.
