// HumanGram-Proxy — локальный прокси MTProto-over-WebSocket.
//
// Лицензия: MIT. Copyright (c) 2026 HumanGram contributors.
//
// Credits:
//   - Flowseal, проект tg-ws-proxy (https://github.com/Flowseal/tg-ws-proxy,
//     MIT) — источник сведений о протоколе локального прокси MTProto,
//     переносящего трафик в WebSocket.
//   - Разработчики Telegram (https://core.telegram.org/mtproto) — протокол
//     MTProto и исходный код клиентов, на совместимость с которыми
//     ориентирована реализация.
//
// Полный текст Credits приведён в LICENSE и README.md.

package main

import "fmt"

// main не вызывается при сборке в режиме c-shared: библиотека
// предоставляет точки входа через C-мост. Функция оставлена, чтобы пакет
// собирался обычной командой `go build ./...` и проходил `go vet`.
func main() {
	fmt.Println("HumanGram-Proxy — это библиотека, а не исполняемый файл.")
	fmt.Println("Используйте cmd/humangram-proxy для запуска из командной строки.")
	fmt.Println("Сборка библиотеки: go build -buildmode=c-shared -o HumanGramProxy.dll ./bridge")
}
