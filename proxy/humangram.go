// Package humangram — встраиваемый локальный прокси MTProto-over-WebSocket.
//
// Пакет предоставляет простой API инициализации: Start поднимает прокси на
// 127.0.0.1 со свободным портом и возвращает адрес, который клиент
// Telegram должен использовать в качестве адреса прокси. Никаких внешних
// утилит и ручной настройки не требуется.
//
// # Схема работы
//
//	Клиент Telegram                HumanGram-Proxy                 Telegram DC
//	        |                              |                              |
//	        | TCP + обфускация MTProto     |                              |
//	        |----------------------------->|                              |
//	        |         64-байтовый префикс    |                              |
//	        |<-----------------------------|                              |
//	        |   кадры MTProto (abridged /   | TCP + обфускация            |
//	        |   intermediate)               | или WebSocket                |
//	        |                              |----------------------------->|
//	        |                              |     кадры MTProto            |
//	        |                              |<-----------------------------|
//
// Прокси прозрачен: ключ авторизации устанавливает сам клиент, и прокси
// не может прочитать содержимое сообщений.
//
// # Лицензия
//
// Код HumanGram-Proxy распространяется по лицензии MIT (файл LICENSE).
// Клиенты Telegram Desktop и Telegram Android, поверх которых собираются
// приложения HumanGram, распространяются по лицензии GPL; см. раздел
// Credits в README.md.
//
// Copyright (c) 2026 HumanGram contributors
package humangram

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/HumanGram/humangram-proxy/internal/dc"
	"github.com/HumanGram/humangram-proxy/internal/proxy"
	"github.com/HumanGram/humangram-proxy/internal/transport"
)

// Version — версия модуля HumanGram-Proxy.
const Version = "1.0.0"

// UpstreamMode задаёт вариант восходящего транспорта.
type UpstreamMode string

const (
	// UpstreamDirect — прямое подключение к дата-центру с обфускацией MTProto.
	UpstreamDirect UpstreamMode = "direct"
	// UpstreamWebSocket — перенос трафика MTProto внутрь WebSocket-соединения.
	UpstreamWebSocket UpstreamMode = "websocket"
)

// Config — конфигурация локального прокси.
type Config struct {
	// Secret — секрет прокси в виде 16 байт, заданных шестнадцатеричной
	// строкой (32 символа). Этот же секрет должен быть указан в настройках
	// клиента Telegram. Пустая строка включает режим abridged без
	// шифрования на стороне клиента.
	Secret string

	// Host — адрес прослушивания. По умолчанию 127.0.0.1.
	Host string

	// Port — порт прослушивания. Ноль означает свободный порт.
	Port int

	// Upstream — режим восходящего транспорта. По умолчанию UpstreamDirect.
	Upstream UpstreamMode

	// WebSocketURL — адрес WebSocket-эндпоинта, например
	// "wss://example.org/mtproto". Используется при UpstreamWebSocket.
	WebSocketURL string

	// WebSocketSubprotocol — значение Sec-WebSocket-Protocol.
	// Согласно спецификации транспортов MTProto по умолчанию "binary".
	WebSocketSubprotocol string

	// InsecureSkipVerify отключает проверку TLS-сертификата
	// WebSocket-эндпоинта.
	InsecureSkipVerify bool

	// DCOverrides задаёт адреса дата-центров в формате
	// {"2": "10.0.0.1:443,10.0.0.2:443"}. Ключ — номер дата-центра.
	DCOverrides map[string]string

	// IPv6Only ограничивает выбор адресов семейством IPv6.
	IPv6Only bool

	// HandshakeTimeout ограничивает установку соединений. Ноль означает
	// 15 секунд.
	HandshakeTimeout time.Duration

	// IdleTimeout закрывает сессию при отсутствии активности.
	// Нулевое значение отключает таймаут.
	IdleTimeout time.Duration

	// Logger получает диагностические сообщения. При nil сообщения
	// отбрасываются.
	Logger *slog.Logger
}

func (c *Config) withDefaults() {
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Upstream == "" {
		c.Upstream = UpstreamDirect
	}
	if c.WebSocketSubprotocol == "" {
		c.WebSocketSubprotocol = "binary"
	}
	if c.HandshakeTimeout <= 0 {
		c.HandshakeTimeout = 15 * time.Second
	}
}

// Proxy — запущенный локальный прокси MTProto.
type Proxy struct {
	server *proxy.Server
	log    *slog.Logger
	cancel context.CancelFunc
	done   chan struct{}
}

// Start запускает локальный прокси и возвращает его дескриптор.
//
// Прокси слушает 127.0.0.1 на свободном порту (или на порту из конфигурации).
// Клиент Telegram должен быть настроен на адрес Addr и секрет из конфигурации.
func Start(ctx context.Context, cfg Config) (*Proxy, error) {
	cfg.withDefaults()

	secret, err := parseSecret(cfg.Secret)
	if err != nil {
		return nil, err
	}

	resolver := dc.NewResolver().WithIPv6Only(cfg.IPv6Only)
	for id, spec := range cfg.DCOverrides {
		var dcID int
		if _, err := fmt.Sscanf(id, "%d", &dcID); err != nil {
			return nil, fmt.Errorf("humangram: некорректный номер дата-центра %q: %w", id, err)
		}
		if err := resolver.WithOverride(dcID, spec); err != nil {
			return nil, err
		}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError}))
	}

	upstream := &transport.Upstream{
		Resolver:              resolver,
		HandshakeTimeout:      cfg.HandshakeTimeout,
		TLSInsecureSkipVerify: cfg.InsecureSkipVerify,
	}
	if cfg.Upstream == UpstreamWebSocket {
		if cfg.WebSocketURL == "" {
			return nil, errors.New("humangram: для WebSocket-транспорта требуется WebSocketURL")
		}
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.InsecureSkipVerify}
		upstream.WS = &transport.WSConfig{
			URL:              cfg.WebSocketURL,
			Subprotocol:      cfg.WebSocketSubprotocol,
			TLSConfig:        tlsCfg,
			HandshakeTimeout: cfg.HandshakeTimeout,
		}
	} else if cfg.Upstream != UpstreamDirect {
		return nil, fmt.Errorf("humangram: неизвестный режим транспорта %q", cfg.Upstream)
	}

	runCtx, cancel := context.WithCancel(ctx)
	srv, err := proxy.New(proxy.Config{
		Host:             cfg.Host,
		Port:             cfg.Port,
		Secret:           secret,
		Upstream:         upstream,
		Logger:           logger,
		HandshakeTimeout: cfg.HandshakeTimeout,
		IdleTimeout:      cfg.IdleTimeout,
		Parent:           runCtx,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	if err := srv.Start(); err != nil {
		cancel()
		return nil, err
	}
	p := &Proxy{server: srv, log: logger, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		if err := srv.Serve(); err != nil {
			logger.Error("HumanGram: прокси остановлен с ошибкой", "err", err)
		}
	}()
	return p, nil
}

// Addr возвращает адрес прокси в формате "host:port".
func (p *Proxy) Addr() string {
	if p.server.Addr() == nil {
		return ""
	}
	return p.server.Addr().String()
}

// Host возвращает адрес прослушивания прокси.
func (p *Proxy) Host() string {
	if p.server.Addr() == nil {
		return ""
	}
	host, _, err := splitHostPort(p.server.Addr().String())
	if err != nil {
		return ""
	}
	return host
}

// Port возвращает номер порта прокси.
func (p *Proxy) Port() int { return p.server.Port() }

// Sessions возвращает число активных сессий.
func (p *Proxy) Sessions() int64 { return p.server.Sessions() }

// Stop останавливает прокси и освобождает ресурсы.
func (p *Proxy) Stop() error {
	err := p.server.Close()
	p.cancel()
	<-p.done
	return err
}

// parseSecret разбирает секрет из шестнадцатеричной строки.
func parseSecret(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	// Поддерживается формат с префиксом secret:/secret- в стиле MTProxy.
	for _, prefix := range []string{"secret-", "secret:"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			break
		}
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("humangram: секрет должен быть шестнадцатеричной строкой: %w", err)
	}
	if len(raw) != 16 {
		return nil, fmt.Errorf("humangram: секрет должен содержать 16 байт (32 символа), получено %d", len(raw))
	}
	return raw, nil
}

func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", "", errors.New("humangram: неверный адрес")
	}
	return strings.Trim(addr[:i], "[]"), addr[i+1:], nil
}

// discard — приёмник, отбрасывающий диагностические сообщения.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
