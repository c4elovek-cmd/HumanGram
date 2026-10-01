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

package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/HumanGram/humangram-proxy/internal/dc"
	"github.com/HumanGram/humangram-proxy/internal/mtproto"
)

// Upstream устанавливает соединения с сервером Telegram.
//
// Поддерживаются два варианта восходящего транспорта:
//
//   - прямой: TCP с обфускацией (поле WS не задано);
//   - WebSocket: обфускация MTProto переносится внутрь WebSocket-соединения,
//     что позволяет обходить сетевое фильтрование, ориентированное на
//     распознавание трафика MTProto.
type Upstream struct {
	// Resolver сопоставляет индекс дата-центра с адресами.
	Resolver *dc.Resolver
	// WS задаёт параметры WebSocket-транспорта. При nil используется
	// прямое подключение по TCP.
	WS *WSConfig
	// Dialer используется для прямых подключений.
	Dialer *net.Dialer
	// HandshakeTimeout ограничивает установку соединения.
	HandshakeTimeout time.Duration
	// TLSInsecureSkipVerify отключает проверку сертификата WebSocket-эндпоинта.
	// Используется только для самоподписанных сертификатов на локальных
	// relay-серверах.
	TLSInsecureSkipVerify bool
}

// Dial реализует интерфейс session.UpstreamDialer.
func (u *Upstream) Dial(ctx context.Context, dcID int, dcIndex int16, tag [4]byte) (net.Conn, error) {
	if u.Resolver == nil {
		u.Resolver = dc.NewResolver()
	}
	endpoints, err := u.Resolver.Resolve(dcID)
	if err != nil {
		return nil, err
	}
	timeout := u.HandshakeTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	relay, err := mtproto.NewRelayInit(tag, dcIndex)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, ep := range endpoints {
		conn, err := u.dialEndpoint(ctx, ep, timeout)
		if err != nil {
			lastErr = err
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(timeout))
		wrapped, err := Handshake(conn, relay)
		if err != nil {
			conn.Close()
			lastErr = fmt.Errorf("%s: %w", ep, err)
			continue
		}
		_ = conn.SetDeadline(time.Time{})
		return wrapped, nil
	}
	if lastErr == nil {
		lastErr = errors.New("нет доступных адресов дата-центра")
	}
	return nil, fmt.Errorf("не удалось подключиться к дата-центру %d: %w", dcID, lastErr)
}

func (u *Upstream) dialEndpoint(ctx context.Context, ep dc.Endpoint, timeout time.Duration) (net.Conn, error) {
	if u.WS == nil {
		dialer := u.Dialer
		if dialer == nil {
			dialer = &net.Dialer{Timeout: timeout}
		}
		return dialer.DialContext(ctx, "tcp", ep.String())
	}

	cfg := *u.WS
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = timeout
	}
	if u.TLSInsecureSkipVerify {
		base := cfg.TLSConfig
		if base == nil {
			base = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			base = base.Clone()
		}
		base.InsecureSkipVerify = true
		cfg.TLSConfig = base
	}
	// При WebSocket-транспорте адрес дата-центра используется как имя
	// виртуального узла: соединение устанавливается с настроенного
	// WebSocket-эндпоинта, а идентификатор дата-центра передаётся
	// в префиксе обфускации.
	return DialWS(ctx, cfg)
}
