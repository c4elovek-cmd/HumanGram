<div align="center">

# HumanGram

**Telegram с встроенным MTProto-over-WebSocket прокси**

Автономные клиенты для Windows и Android, в которых логика обхода блокировок
встроена прямо в бинарный файл. Никаких сторонних утилит, никакой ручной
настройки прокси.

</div>

---

## Что это такое

HumanGram — модифицированный клиент Telegram, в который встроен локальный
прокси MTProto-over-WebSocket по принципу проекта
[Flowseal/tg-ws-proxy](https://github.com/Flowseal/tg-ws-proxy).

При запуске приложение поднимает прокси на `127.0.0.1` со свободным портом и
само подключает к нему транспорт MTProto. Пользователю не нужно ни устанавливать
сторонние программы, ни вручную прописывать адрес и порт прокси.

Ключевое отличие подхода: клиент **не меняет** адреса дата-центров Telegram
и **не** перенаправляет трафик на сторонний сервер. Маршрутизация остаётся
прямой, поэтому сохраняются все штатные механизмы Telegram — коды входа,
двухфакторная аутентификация, push-уведомления, работа с медиа.

### Схема работы

```
┌──────────────────────┐                    ┌────────────────────┐
│  HumanGram Desktop   │                    │  Серверы Telegram  │
│  или Android         │                    │                    │
│                      │                    │                    │
│  ┌────────────────┐  │   TCP + обфускация │                    │
│  │HumanGram-Proxy │  │   MTProto (клиент → │                    │
│  │  127.0.0.1:*   │──┼──▶  127.0.0.1)      │                    │
│  └────────────────┘  │                    │                    │
│                      │   TCP + обфускация │                    │
│                      │   MTProto (прокси → │                    │
│                      │   сервер) или       │                    │
│                      │   WebSocket (wss)   │                    │
│                      │────────────────────┼───────────────────▶│
└──────────────────────┘                    └────────────────────┘
```

### Приватность

Прокси **прозрачен**. Ключ авторизации (`auth_key`) устанавливает сам клиент
в ходе рукопожатия MTProto, и прокси в это рукопожатие не вмешивается: ключ
никогда не покидает память клиента. Прокси снимает обфускацию и кадрирование
на стороне клиента и заново применяет их на стороне сервера, формируя
собственные размеры кадров и выравнивание. Прочитать содержимое переписки
прокси технически не может.

---

## Состав репозитория

| Путь | Описание | Лицензия |
|---|---|---|
| `proxy/` | **HumanGram-Proxy** — модуль прокси на Go | MIT |
| `build/` | скрипты сборки и установки | MIT |
| `desktop/` | исходники Telegram Desktop для сборки HumanGram Desktop | GPL-3.0 |
| `android/` | исходники Telegram Android для сборки HumanGram Android | GPL-2.0 |

Один исходный код модуля собирается в три артефакта:

| Артефакт | Платформа | Назначение |
|---|---|---|
| `HumanGramProxy.dll` | Windows x64 | встраивается в HumanGram Desktop |
| `libhumangramproxy.so` | Android arm64-v8a | встраивается в HumanGram Android |
| `humangram-proxy.exe` | Windows x64 | диагностика и запуск из командной строки |

Подробное описание модуля, его API и всех ключей конфигурации —
в [`proxy/README.md`](proxy/README.md).

---

## Сборка

### Предварительные требования

Все действия выполняются на диске **G**, диск C не используется.

| Компонент | Назначение |
|---|---|
| Go 1.27+ | сборка HumanGram-Proxy |
| gcc (MinGW-w64) | сборка `HumanGramProxy.dll` через cgo |
| Android SDK + NDK | сборка `libhumangramproxy.so` и HumanGram Android |
| JDK 17 | сборка HumanGram Android |
| Visual Studio Build Tools 2022/2026 + CMake | сборка HumanGram Desktop |

### 1. HumanGram-Proxy

```powershell
# Windows: C-компилятор для сборки общей библиотеки
winget install BrechtSanders.WinLibs.POSIX.UCRT

# Android: NDK и CMake на диск G
pwsh -File build\install-android-sdk.ps1

# Сборка всех артефактов модуля
pwsh -File build\build-proxy.ps1
```

### 2. HumanGram Desktop (Windows)

```powershell
pwsh -File build\build-desktop.ps1
```

### 3. HumanGram Android (arm64-v8a)

```powershell
pwsh -File build\build-android.ps1
```

### Сборка всего сразу

```powershell
pwsh -File build\build-all.ps1
```

---

## Проверка модуля

Тесты не требуют доступа к сети:

```powershell
cd proxy
go vet ./...
go test ./... -count=1
```

Что проверяется:

* `internal/mtproto` — совместимость с эталонной реализацией стороны клиента
  из tdesktop: разбор префикса обфускации, вывод ключей, определение
  дата-центра, кадрирование во всех трёх протоколах;
* `internal/proxy` — сквозной прогон: поднимается эмулятор сервера Telegram,
  через прокси проходят реальные кадры в обоих направлениях, включая
  передачу 512 КиБ, серию кадров, отказ постороннему трафику и корректность
  остановки при активной сессии.

---

## Credits

Проект HumanGram опирается на работу следующих авторов и проектов. Мы
выражаем им искреннюю признательность.

### Telegram

Команда разработчиков Telegram — авторы протокола MTProto и его открытой
спецификации, а также исходного кода клиентов, поверх которых собираются
приложения HumanGram.

* [core.telegram.org/mtproto](https://core.telegram.org/mtproto) — спецификация протокола
* [Telegram Desktop](https://github.com/telegramdesktop/tdesktop) — GPL-3.0-or-later
* [Telegram Android (TMPro)](https://github.com/DrKLO/Telegram) — GPL-2.0
* [Telegram Web](https://github.com/microsoft/gramjs) и
  [MadelineProto](https://github.com/danog/MadelineProto) — примеры реализации
  транспорта WebSocket, на которые ссылается спецификация

Telegram Desktop и Telegram Android распространяются по лицензиям GPL.
Настоящий проект — модифицированная версия этих клиентов, поэтому
приложения HumanGram распространяются на условиях GPL-3.0 (Desktop)
и GPL-2.0 (Android). Лицензия MIT в корне репозитория распространяется
только на оригинальные материалы HumanGram — модуль прокси, скрипты сборки
и документацию.

### Flowseal — tg-ws-proxy

**Flowseal** — автор проекта
[Flowseal/tg-ws-proxy](https://github.com/Flowseal/tg-ws-proxy), лицензия
MIT, Copyright (c) 2026 Flowseal. Этот проект — источник самой идеи
HumanGram: локальный прокси, который принимает подключение клиента Telegram
на loopback и переносит трафик MTProto в WebSocket. Сведения о протоколе,
взятье из этого проекта, использованы при написании модуля HumanGram-Proxy.
Модуль HumanGram-Proxy написан заново на Go, в объёме, пригодном для
встраивания в клиент, и не является производным от Python-кода tg-ws-proxy.

### Форки Telegram

При выборе точек интеграции учитывался опыт следующих форков. Их код
не включён в состав настоящего репозитория, но решения и подходы,
выработанные их авторами, повлияли на архитектуру HumanGram.

* **AyuGram** — [AyuGram/tdesktop](https://github.com/AyuGram/tdesktop),
  GPL-3.0-or-later. Расширения клиента, встраиваемые в его кодовую базу.
* **Nekogram** — [Nekogram/Nekogram](https://github.com/Nekogram/Nekogram),
  GPL-3.0-or-later. Альтернативная ветвь развития клиента для Android и
  десктопа.
* **Forkgram** и другие форки tdesktop — GPL-3.0-or-later.

### Порты прокси на Go и Rust

Идея локального MTProto-прокси с WebSocket-транспортом получила
распространение в виде портов на других языках. Авторы этих портов
заслуживают отдельной благодарности за независимую проработку протокола;
их код в HumanGram **не** используется, поскольку лицензии конкретных
репозиториев не проверялись. Собственная реализация HumanGram-Proxy
написана с нуля по открытой спецификации MTProto.

* Порты на **Go** — сообщество MTProto-инструментов на Go (например,
  [gotd/td](https://github.com/gotd/td), MIT) — источник сведений о
  практической реализации рукопожатия MTProto.
* Порты на **Rust** — реализации MTProto на Rust и
  [telegram-ws-proxy](https://github.com/raztso/telegram-ws-proxy) (MIT).

### Прочее

* **Go** и стандартная библиотека Go — https://go.dev
* Формат WebSocket — RFC 6455 (IETF), рекомендации W3C
* MD5 в WebSocket — RFC 6455, Appendix 1

---

## Лицензия

* **HumanGram-Proxy**, скрипты сборки, документация — **MIT**, файл
  [`LICENSE`](LICENSE).
* **HumanGram Desktop** (производный от Telegram Desktop) — **GPL-3.0-or-later**,
  файл `desktop/LICENSE`.
* **HumanGram Android** (производный от Telegram Android) — **GPL-2.0**,
  файл `android/LICENSE`.

Распространяя бинарные файлы клиентов, мы предоставляем соответствующий
исходный код в этом репозитории, как того требуют обе лицензии GPL.
