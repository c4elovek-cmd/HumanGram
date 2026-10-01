// Package main — C-мост к HumanGram-Proxy.
//
// Собирается как общая библиотека (shared library) и предоставляет
// C-совместимый API для вызова из клиента Telegram:
//
//	go build -buildmode=c-shared -o HumanGramProxy.dll ./bridge
//	go build -buildmode=c-shared -o libhumangramproxy.so ./bridge
//
// Данный файл распространяется по лицензии MIT.
// Copyright (c) 2026 HumanGram contributors
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/HumanGram/humangram-proxy"
)

var (
	// errAlreadyRunning возвращается при повторном запуске прокси.
	errAlreadyRunning = errors.New("HumanGram-Proxy: прокси уже запущен")
	// errNilConfig возвращается, если конфигурация не передана.
	errNilConfig = errors.New("HumanGram-Proxy: не передана конфигурация")
)

// newBridgeLogger создаёт журнал, пишущий в стандартный поток ошибок,
// чтобы диагностика попадала в консоль клиента.
func newBridgeLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// humanGramBridgeConfig — представление Config в JSON.
// В cgo-мосте удобнее передавать конфигурацию одной строкой, чем
// описывать структуру на C.
type bridgeConfig struct {
	Secret               string            `json:"secret"`
	Host                 string            `json:"host"`
	Port                 int               `json:"port"`
	Upstream             string            `json:"upstream"`
	WebSocketURL         string            `json:"websocketUrl"`
	WebSocketSubprotocol string            `json:"websocketSubprotocol"`
	InsecureSkipVerify   bool              `json:"insecureSkipVerify"`
	DCOverrides          map[string]string `json:"dcOverrides"`
	IPv6Only             bool              `json:"ipv6Only"`
	HandshakeTimeoutMs   int               `json:"handshakeTimeoutMs"`
	IdleTimeoutMs        int               `json:"idleTimeoutMs"`
	Debug                bool              `json:"debug"`
}

var (
	mu      sync.Mutex
	running *humangram.Proxy
	lastErr string
)

// setErr запоминает последнюю ошибку для HumanGramProxyLastError.
func setErr(err error) {
	mu.Lock()
	defer mu.Unlock()
	if err == nil {
		lastErr = ""
		return
	}
	lastErr = err.Error()
}

// HumanGramProxyVersion возвращает версию модуля как строку в формате UTF-8.
// Возвращаемая память действительна до вызова HumanGramProxyFreeString.
//
//export HumanGramProxyVersion
func HumanGramProxyVersion() *C.char {
	return C.CString(humangram.Version)
}

// HumanGramProxyLastError возвращает описание последней ошибки.
// Возвращаемая память действительна до вызова HumanGramProxyFreeString.
//
//export HumanGramProxyLastError
func HumanGramProxyLastError() *C.char {
	mu.Lock()
	defer mu.Unlock()
	return C.CString(lastErr)
}

// HumanGramProxyFreeString освобождает строку, полученную от модуля.
//
//export HumanGramProxyFreeString
func HumanGramProxyFreeString(s *C.char) {
	C.free(unsafe.Pointer(s))
}

// HumanGramProxyStart запускает локальный прокси по конфигурации в JSON.
// Возвращает 0 в случае успеха и ненулевой код ошибки в противном случае:
// 2 — прокси уже запущен, 3 — не передана конфигурация,
// 4 — конфигурация повреждена, 5 — не удалось запустить прокси.
//
//export HumanGramProxyStart
func HumanGramProxyStart(configJSON *C.char) C.int {
	mu.Lock()
	if running != nil {
		mu.Unlock()
		setErr(errAlreadyRunning)
		return 2
	}
	mu.Unlock()

	if configJSON == nil {
		setErr(errNilConfig)
		return 3
	}
	raw := C.GoString(configJSON)

	var bc bridgeConfig
	if err := json.Unmarshal([]byte(raw), &bc); err != nil {
		setErr(err)
		return 4
	}

	cfg := humangram.Config{
		Secret:               strings.TrimSpace(bc.Secret),
		Host:                 bc.Host,
		Port:                 bc.Port,
		Upstream:             humangram.UpstreamMode(bc.Upstream),
		WebSocketURL:         bc.WebSocketURL,
		WebSocketSubprotocol: bc.WebSocketSubprotocol,
		InsecureSkipVerify:   bc.InsecureSkipVerify,
		DCOverrides:          bc.DCOverrides,
		IPv6Only:             bc.IPv6Only,
		HandshakeTimeout:     millis(bc.HandshakeTimeoutMs),
		IdleTimeout:          millis(bc.IdleTimeoutMs),
	}
	if cfg.Upstream == "" {
		cfg.Upstream = humangram.UpstreamDirect
	}
	if bc.Debug {
		cfg.Logger = newBridgeLogger()
	}

	p, err := humangram.Start(context.Background(), cfg)
	if err != nil {
		setErr(err)
		return 5
	}
	mu.Lock()
	running = p
	mu.Unlock()
	setErr(nil)
	return 0
}

// HumanGramProxyAddr возвращает адрес прокси в формате "host:port".
//
//export HumanGramProxyAddr
func HumanGramProxyAddr() *C.char {
	mu.Lock()
	p := running
	mu.Unlock()
	if p == nil {
		return C.CString("")
	}
	return C.CString(p.Addr())
}

// HumanGramProxyPort возвращает номер порта прокси либо 0, если прокси
// не запущен.
//
//export HumanGramProxyPort
func HumanGramProxyPort() C.int {
	mu.Lock()
	p := running
	mu.Unlock()
	if p == nil {
		return 0
	}
	return C.int(p.Port())
}

// HumanGramProxySessions возвращает число активных сессий.
//
//export HumanGramProxySessions
func HumanGramProxySessions() C.longlong {
	mu.Lock()
	p := running
	mu.Unlock()
	if p == nil {
		return 0
	}
	return C.longlong(p.Sessions())
}

// HumanGramProxyStop останавливает прокси. Возвращает 0 при успехе.
//
//export HumanGramProxyStop
func HumanGramProxyStop() C.int {
	mu.Lock()
	p := running
	running = nil
	mu.Unlock()
	if p == nil {
		return 0
	}
	if err := p.Stop(); err != nil {
		setErr(err)
		return 1
	}
	setErr(nil)
	return 0
}

// HumanGramProxyIsRunning сообщает, запущен ли прокси: 1 — да, 0 — нет.
//
//export HumanGramProxyIsRunning
func HumanGramProxyIsRunning() C.int {
	mu.Lock()
	defer mu.Unlock()
	if running == nil {
		return 0
	}
	return 1
}

func millis(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
