# HumanGram v1.0.0

Telegram с встроенным локальным прокси MTProto-over-WebSocket.

## Состав выпуска

| Файл | Описание | Состояние |
|---|---|---|
| `HumanGram-Android.apk` | Клиент для Android, universal (arm64-v8a, armeabi-v7a, x86, x86_64) со встроенным прокси | проверен |
| `HumanGramProxy-Windows-x64.zip` | Модуль прокси для Windows: `HumanGramProxy.dll`, заголовок, утилита командной строки | проверен |
| `HumanGram-Proxy-Android-ABIs.zip` | Нативная библиотека прокси для Android отдельно, для ручной интеграции | проверен |

### Ожидается в этом же выпуске

`HumanGram-Windows.zip` — клиент Telegram Desktop, собранный из
исходников с встроенным прокси. **Сборка продолжается** и будет
добавлена в этот выпуск отдельным вложением. Причина задержки —
подготовка 33 сторонних зависимостей (Qt, ffmpeg, OpenSSL), часть
которых загружается с зеркал, недоступных из некоторых сетей.

Подробности сборки и текущий статус: `docs/BUILD-PATCHES.md` и
`docs/INTERRUPTIBLE-BUILD.md` в репозитории.

## Что проверено в Android-апке

| Проверка | Результат |
|---|---|
| Подпись | `CN=HumanGram`, алгоритм SHA256withRSA |
| Имя приложения | `HumanGram` |
| Идентификатор | `com.humangram.messenger` |
| Версия | 1.0.0 (`versionCode` 110009) |
| Объявленные архитектуры | `arm64-v8a`, `armeabi-v7a`, `x86`, `x86_64` |
| Нативные библиотеки | по 3 в каждой архитектуре: `libtmessages.49.so`, `libhumangramproxy.so`, `liblanguage_id_l2c_jni.so` |
| Размер | 71,8 МБ |

Ключ подписи — собственный (`CN=HumanGram`). Официальный ключ Telegram
из исходного проекта не используется: подпись модифицированного
приложения чужим ключом сделала бы сборки неотличимыми от официальных.

## Как это работает

```
Клиент Telegram              HumanGram-Proxy            Серверы Telegram
       |                            |                           |
       | TCP + обфускация MTProto   |                           |
       |--------------------------->|                           |
       |   64-байтовый префикс      | TCP + обфускация MTProto |
       |<---------------------------| или WebSocket (wss)       |
       |  кадры MTProto             |-------------------------->|
       |                            |                           |
       |                            |<--------------------------|
```

Прокси прозрачен. Ключ авторизации (`auth_key`) устанавливает сам
клиент, и прокси в рукопожатие не вмешивается: ключ не покидает память
клиента, поэтому прокси технически не может прочитать переписку.
Идентификатор дата-центра клиент передаёт в начальном префиксе,
поэтому адреса настраивать не требуется.

## Использование модуля прокси отдельно

```c
#include "HumanGramProxy.h"

const char *cfg = "{\"secret\":\"00112233445566778899aabbccddeeff\",\"port\":0}";
if (HumanGramProxyStart(cfg) != 0) {
    fprintf(stderr, "%s\n", HumanGramProxyLastError());
    return 1;
}
const char *addr = HumanGramProxyAddr();   /* освобождается HumanGramProxyFreeString */
int port = HumanGramProxyPort();
```

Полное описание API и ключей конфигурации — в `proxy/README.md`.

## Лицензия

* HumanGram-Proxy, скрипты сборки, документация — **MIT**.
* HumanGram Android (производный от Telegram Android) — **GPL-2.0**.
* Исходный код клиента приведён в репозитории в виде патчей
  (`docs/patches/`), что соответствует требованию GPL об предоставлении
  исходников.

## Credits

* **Flowseal** — автор [tg-ws-proxy](https://github.com/Flowseal/tg-ws-proxy)
  (MIT, Copyright (c) 2026 Flowseal): идея локального прокси,
  переносящего трафик MTProto в WebSocket, и сведения о протоколе.
* **Разработчики Telegram** — протокол MTProto и исходный код клиентов.
* Форки **AyuGram**, **Nekogram**, **Forkgram** — повлияли на выбор
  точек интеграции.
* Сообщество **Go** — стандартная библиотека.
