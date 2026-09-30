# Правки сборки HumanGram

Проект HumanGram собирается поверх исходников Telegram Desktop и
Telegram Android. Эти проекты разрабатываются в среде, которая может
отличаться от текущей, поэтому иногда требуются локальные правки
сборочных скриптов. Ниже перечислены все внесённые изменения: они
воспроизводимы и задокументированы, чтобы сборка была повторяемой.

Каждая правка меняет только скрипты подготовки зависимостей и сборки.
Исходный код клиентов не меняется, кроме явно указанных интеграций
HumanGram.

---

## 1. `desktop/Telegram/build/prepare/prepare.py`

### 1.1. Пакеты MSYS2 (репозиторий `msys64`)

**Причина.** Сборочные скрипты запрашивают пакеты
`mingw-w64-x86_64-{diffutils,gperf,nasm,perl,pkgconf}`. В текущем
состоянии репозитория MSYS2 эти имена отсутствуют: единый репозиторий
`mingw-w64-x86_64-*` был разделён на `ucrt64`, `clang64` и `clangarm64`.
Установка падает с ошибкой
`error: target not found: mingw-w64-x86_64-diffutils`.

**Изменение.** Запрошены пакеты из основного репозитория `msys`, которые
доступны и попадают в `msys64/usr/bin` — именно этот каталог добавляется
в `PATH` на данной стадии.

```diff
-bash -c "pacman-key --init; pacman-key --populate; pacman -Syu --noconfirm"
-    pacman -Syu --noconfirm ^
-        make ^
-        mingw-w64-x86_64-diffutils ^
-        mingw-w64-x86_64-gperf ^
-        mingw-w64-x86_64-nasm ^
-        mingw-w64-x86_64-perl ^
-        mingw-w64-x86_64-pkgconf
+bash -c "pacman-key --init; pacman-key --populate; pacman -Syyuu --noconfirm"
+    pacman -Syu --noconfirm
+    pacman -S --noconfirm --needed ^
+        make ^
+        diffutils ^
+        gperf ^
+        nasm ^
+        perl ^
+        pkgconf
```

### 1.2. Каталоги MSYS2 в `PATH`

**Причина.** `pathPrefixes` содержит только `ThirdParty\msys64\mingw64\bin`.
Пакеты, устанавливаемые на стадии `msys64`, при этом попадают в
`msys64\usr\bin`, поэтому инструменты (`perl`, `make`, `diff`, `gperf`,
`nasm`, `pkgconf`) не находятся. Сборка OpenSSL падает с сообщением
`'perl' is not recognized as an internal or external command`.

**Изменение.** В `PATH` добавляется каталог `usr\bin`.

```diff
 pathPrefixes = [
     'ThirdParty\\msys64\\mingw64\\bin',
+    'ThirdParty\\msys64\\usr\\bin',
     'ThirdParty\\jom',
     'ThirdParty\\gyp',
 ] if win else [
```

### 1.3. Набор инструментов MSVC для стадий `lzma` и `breakpad`

**Причина.** Проекты `LzmaLib.sln` и `dump_syms.vcxproj` закрепляют
`PlatformToolset=v143` (Visual Studio 2022). Установленный набор
инструментов — `v145` (Visual Studio 2026), поэтому сборка падает с
ошибкой `MSB8020: не удается найти средства сборки для Visual Studio
2022 (набор инструментов платформы = "v143")`.

**Изменение.** Явно задаётся `v145` через `ToolsetProp`, тем же
способом, которым upstream уже задаёт его для платформы `winarm`.

```diff
     cd lzma\C\Util\LzmaLib
-    SET "ToolsetProp="
+    SET "ToolsetProp=/property:PlatformToolset=v145"
 winarm:
     SET "ToolsetProp=/property:PlatformToolset=v145"
```

Аналогично для стадии `breakpad`:

```diff
     SET "FolderPostfix="
-    SET "ToolsetProp="
+    SET "ToolsetProp=/property:PlatformToolset=v145"
 win64:
     SET "FolderPostfix=_x64"
```

**Примечание.** Альтернатива — установить набор инструментов 14.44
компонентом `Microsoft.VisualStudio.Component.VC.14.44.17.14.x86.x64`
и собирать через `vcvars64.bat -vcvars_ver=14.44`, как рекомендует
`docs/building-win.md`. Установка компонента на этой машине завершается
ошибкой, поэтому применён первый способ.

---

## 2. `android/TMessagesProj/src/main/res/values/strings.xml`

**Изменение.** Отображаемое имя приложения.

```diff
-<string name="AppName">Telegram</string>
+<string name="AppName">HumanGram</string>
```

---

## 3. `android/TMessagesProj/src/main/java/org/telegram/messenger/HumanGramProxy.java`

Новый файл: Java-обвязка нативного модуля HumanGram-Proxy. Загружает
`libhumangramproxy.so`, поднимает локальный прокси и хранит настройки
в приватном хранилище приложения.

Если библиотека недоступна (например, сборка под другую архитектуру),
приложение продолжает работать с прямыми подключениями: ошибка
обрабатывается, а не пробрасывается.

---

## 4. `android/TMessagesProj/src/main/java/org/telegram/messenger/ApplicationLoader.java`

**Изменение.** Прокси поднимается при создании приложения — до
инициализации нативных библиотек и установки сетевых соединений,
чтобы адрес прокси был известен к моменту создания соединений.

```diff
 if (applicationContext == null) {
     applicationContext = getApplicationContext();
 }

+// HumanGram: поднимаем встроенный локальный прокси MTProto ...
+try {
+    final HumanGramProxy humanGramProxy = HumanGramProxy.start(applicationContext);
+    if (humanGramProxy != null) {
+        AndroidUtilities.setHumanGramProxy(
+                humanGramProxy.getAddress(), HumanGramProxy.getSecret(applicationContext));
+    }
+} catch (Throwable e) {
+    if (BuildVars.LOGS_ENABLED) {
+        FileLog.e(e);
+    }
+}
+
 NativeLoader.initNativeLibs(ApplicationLoader.applicationContext);
```

---

## 5. `android/TMessagesProj/src/main/java/org/telegram/tgnet/ConnectionsManager.java`

**Изменение.** Встроенный прокси получает приоритет над пользовательскими
настройками прокси, поскольку клиент настраивается на него автоматически.
Если встроенный прокси недоступен, поведение остаётся прежним.

```diff
 final ProxySettings proxySettings = ProxySettings.fromSharedPreferences(preferences);
-if (preferences.getBoolean("proxy_enabled", false) && proxySettings.isValid()) {
+final HumanGramProxy humanGramProxy = HumanGramProxy.getInstance();
+if (humanGramProxy != null && humanGramProxy.getPort() > 0) {
+    native_setProxySettings(currentAccount, "127.0.0.1", humanGramProxy.getPort(), "", "",
+            HumanGramProxy.getSecret(ApplicationLoader.applicationContext));
+} else if (preferences.getBoolean("proxy_enabled", false) && proxySettings.isValid()) {
     ...
 }
```

---

## 6. Нативная библиотека

Файл `libhumangramproxy.so` копируется в
`android/TMessagesProj/jni/arm64-v8a/`. Каталог `jni/` объявлен в
`TMessagesProj/build.gradle` как `sourceSets.main.jniLibs.srcDirs`, поэтому
дополнительных изменений сборочной конфигурации не требуется.

Сборка библиотеки выполняется скриптом `build/build-proxy.ps1`.

---

## 7. `desktop/Telegram/SourceFiles/core/version.h`

**Изменение.** Отображаемое имя приложения, имя файла и идентификатор
для системы обновлений.

```diff
-constexpr auto AppId = "{53F49750-6209-4FBF-9CA8-7A333C87D1ED}"_cs;
+constexpr auto AppId = "{9A4F1C77-5B2E-4E38-9C6D-71E0F3A28B54}"_cs;
 constexpr auto AppNameOld = "Telegram Win (Unofficial)"_cs;
-constexpr auto AppName = "Telegram Desktop"_cs;
-constexpr auto AppFile = "Telegram"_cs;
+constexpr auto AppName = "HumanGram"_cs;
+constexpr auto AppFile = "HumanGram"_cs;
```

**Обоснование.** `AppId` используется системой обновлений и в
установщике. Если оставить исходное значение, обновление HumanGram
предложило бы установку поверх Telegram либо, наоборот, Telegram обновил
бы сборку HumanGram. Новый идентификатор разделяет две линии сборок.

---

## 8. `android/gradlew.bat`

**Причина.** В исходном проекте присутствует только вариант обёртки
Gradle для Unix (`gradlew`), вариант для Windows отсутствует.

**Изменение.** Добавлена стандартная Windows-обёртка, запускающая
`org.gradle.wrapper.GradleWrapperMain` из `gradle/wrapper/gradle-wrapper.jar`.

---

## 9. Ключ подписи Android

Официальный ключ подписи Telegram (`TMessagesProj/config/release.keystore`,
`CN=Android Developer, OU=Telegram`) присутствует в исходном проекте и
**не используется**: он удаляется из дерева сборки, а вместо него
создаётся собственный ключ `CN=HumanGram`.

Причина: подпись модифицированного приложения чужим ключом сделала бы
сборки неотличимыми от официальных приложений Telegram.

Учётные данные подписи передаются Gradle флагами `-P` из
`build/build-android.ps1` и не сохраняются в репозитории.

---

## 10. Ограничение архитектуры Android

В `TMessagesProj/build.gradle` в `defaultConfig` добавлено:

```diff
+ndk {
+    abiFilters 'arm64-v8a'
+}
```

Библиотека HumanGram-Proxy поставляется для `arm64-v8a`. Ограничение
сокращает время сборки примерно вчетверо и исключает появление вариантов
без встроенного прокси.

---

## 11. Повторяющиеся сбои сети

Сеть в среде сборки нестабильна: клонирование репозиториев, загрузка
архивов и разрешение плагинов периодически завершаются ошибками TLS
(`schannel: failed to receive handshake`, `The remote name could not be
resolved`). Это не дефекты проекта.

Скрипт `build/prepare-desktop-retry.ps1` повторяет подготовку
зависимостей: `prepare.py` помечает готовые стадии как `SKIPPING`,
поэтому повторный запуск продолжает с места сбоя и не повторяет
выполненную работу.

## 12. `jom`: недоступный хост в среде сборки

**Симптом.** Стадия `[5/33](ThirdParty/jom)` падает на каждой попытке:

```
[5/33](ThirdParty/jom): FAILED
iwr : WebException / удалённое имя не может быть разрешено
```

**Причина.** Из всех 33 стадий подготовки заблокированный сетевой
политикой хост использует ровно одна — загрузка `jom`:

| Хост | DNS | TCP 443 | Используется в prepare.py |
|---|---|---|---|
| `master.qt.io` | 77.86.162.1 | **заблокирован** | да, строка 508 |
| `download.qt.io` | 77.86.162.2 | **заблокирован** | нет |
| `deps.telegram.org` | 149.154.167.99 | **заблокирован** | нет |
| `github.com` | — | доступен | да, основная часть |

`deps.telegram.org` и `download.qt.io` в скриптах не используются, Qt
собирается из исходников с GitHub. Единственная зависимость от
недоступного хоста — бинарный файл `jom`, распространяемый только через
`qt.io` и отсутствующий на зеркалах (проверены `qt.mirror.constant.com`,
`mirrors.ocf.berkeley.edu`, TUNA, USTC, SJTUG, freedif, init7, fau,
dotsrc — везде 404; максимальная доступная версия Qt на зеркалах —
5.12.12 при требуемой 5.15.19).

**Возможные решения.**

1. Обеспечить доступ к `master.qt.io` — тогда подготовка проходит без
   изменений.
2. Заменить `jom` на `nmake` (входит в Visual Studio Build Tools,
   дополнительных загрузок не требуется). Работоспособно, но Qt 5.15.19
   затем собирается последовательно, а не параллельно, что увеличивает
   время подготовки многократно.

**Статус.** Решение за пользователем сборки; остальные зависимости от
сети устранены.

---

## Credits

Правки касаются только сборочных скриптов. Авторство исходных проектов
и их лицензии (GPL-3.0 для Telegram Desktop, GPL-2.0 для Telegram
Android) сохраняются; общий список авторов приведён в корневом файле
`LICENSE` и `README.md`.
