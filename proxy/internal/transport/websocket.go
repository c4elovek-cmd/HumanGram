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
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Операции WebSocket согласно RFC 6455, раздел 7.4.1.
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// wsMagic — константа из RFC 6455 для вычисления Sec-WebSocket-Accept.
const wsMagic = "258EAFA5-E914-47DA-95CA-5AB0DC85B11A"

// maxFrameSize ограничивает размер одного кадра WebSocket (16 МиБ).
const maxFrameSize = 16 << 20

var (
	// ErrWSHandshake возвращается, если сервер отклонил апгрейд соединения.
	ErrWSHandshake = errors.New("transport: неудачное рукопожатие WebSocket")
	// ErrWSProtocol возвращается при нарушении протокола WebSocket.
	ErrWSProtocol = errors.New("transport: нарушение протокола WebSocket")
)

// WSConfig описывает параметры подключения к WebSocket-эндпоинту.
type WSConfig struct {
	// URL — адрес эндпоинта, например "wss://example.org/mtproto".
	// Схема wss включает TLS.
	URL string
	// TLSConfig настраивает TLS-соединение. При nil используются
	// системные корневые сертификаты.
	TLSConfig *tls.Config
	// Subprotocol — значение заголовка Sec-WebSocket-Protocol.
	// Согласно спецификации транспортов MTProto должно быть "binary".
	Subprotocol string
	// HandshakeTimeout ограничивает установку соединения.
	HandshakeTimeout time.Duration
	// Dialer используется для TCP-подключения. При nil применяется
	// значение по умолчанию.
	Dialer *net.Dialer
	// ServerNameOverride позволяет указать имя для проверки сертификата
	// TLS, отличное от имени узла в URL.
	ServerNameOverride string
}

// WSConn — двунаправленный поток байтов, переносимый двоичными кадрами
// WebSocket. Согласно спецификации транспортов MTProto, кадрирование
// WebSocket не используется: данные всех кадров образуют единый поток,
// идентичный потоку TCP.
type WSConn struct {
	conn   net.Conn
	reader *bufio.Reader

	writeMu sync.Mutex
	wbuf    []byte
}

// DialWS устанавливает соединение с WebSocket-эндпоинтом.
func DialWS(ctx context.Context, cfg WSConfig) (*WSConn, error) {
	if cfg.URL == "" {
		return nil, errors.New("transport: не задан адрес WebSocket-эндпоинта")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("transport: разбор адреса %q: %w", cfg.URL, err)
	}
	secure := false
	switch strings.ToLower(u.Scheme) {
	case "wss", "https":
		secure = true
	case "ws", "http":
		secure = false
	default:
		return nil, fmt.Errorf("transport: недопустимая схема %q", u.Scheme)
	}
	host := u.Host
	if u.Port() == "" {
		if secure {
			host = net.JoinHostPort(u.Hostname(), "443")
		} else {
			host = net.JoinHostPort(u.Hostname(), "80")
		}
	}

	timeout := cfg.HandshakeTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	dialer := cfg.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: timeout}
	} else if dialer.Timeout == 0 {
		dialer = &net.Dialer{
			Timeout:   timeout,
			KeepAlive: dialer.KeepAlive,
			Resolver:  dialer.Resolver,
		}
	}

	var conn net.Conn
	if secure {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.TLSConfig != nil {
			tlsCfg = cfg.TLSConfig.Clone()
		}
		switch {
		case cfg.ServerNameOverride != "":
			tlsCfg.ServerName = cfg.ServerNameOverride
		case tlsCfg.ServerName == "":
			tlsCfg.ServerName = u.Hostname()
		}
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsCfg}).DialContext(ctx, "tcp", host)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", host)
	}
	if err != nil {
		return nil, fmt.Errorf("transport: подключение к %s: %w", host, err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	ws := &WSConn{conn: conn, reader: bufio.NewReaderSize(conn, 32*1024)}
	if err := ws.handshake(u, cfg.Subprotocol); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ws, nil
}

func (c *WSConn) handshake(u *url.URL, subprotocol string) error {
	keyRaw := make([]byte, 16)
	if _, err := rand.Read(keyRaw); err != nil {
		return fmt.Errorf("transport: генерация ключа WebSocket: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(keyRaw)

	requestURI := u.RequestURI()
	if requestURI == "" {
		requestURI = "/"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\n", requestURI)
	fmt.Fprintf(&b, "Host: %s\r\n", u.Host)
	b.WriteString("Upgrade: websocket\r\n")
	b.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&b, "Sec-WebSocket-Key: %s\r\n", key)
	b.WriteString("Sec-WebSocket-Version: 13\r\n")
	if subprotocol != "" {
		fmt.Fprintf(&b, "Sec-WebSocket-Protocol: %s\r\n", subprotocol)
	}
	b.WriteString("\r\n")
	if _, err := c.conn.Write([]byte(b.String())); err != nil {
		return fmt.Errorf("transport: отправка рукопожатия WebSocket: %w", err)
	}

	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
	resp, err := http.ReadResponse(c.reader, req)
	if err != nil {
		return fmt.Errorf("transport: чтение ответа рукопожатия: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("%w: статус %d", ErrWSHandshake, resp.StatusCode)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		return fmt.Errorf("%w: отсутствует заголовок Upgrade", ErrWSHandshake)
	}
	sum := sha1.Sum([]byte(key + wsMagic))
	if resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		return fmt.Errorf("%w: неверный Sec-WebSocket-Accept", ErrWSHandshake)
	}
	return nil
}

// Read реализует net.Conn поверх кадров WebSocket.
func (c *WSConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Данные, оставшиеся от предыдущего кадра.
	if len(c.wbuf) > 0 {
		n := copy(p, c.wbuf)
		c.wbuf = c.wbuf[n:]
		return n, nil
	}
	opcode, payload, err := c.readFrame()
	if err != nil {
		return 0, err
	}
	if opcode != opBinary && opcode != opText && opcode != opContinuation {
		return 0, fmt.Errorf("%w: неожиданный код операции 0x%x", ErrWSProtocol, opcode)
	}
	n := copy(p, payload)
	if n < len(payload) {
		c.wbuf = payload[n:]
	}
	return n, nil
}

// Write реализует net.Conn: отправляет данные двоичным кадром WebSocket.
func (c *WSConn) Write(p []byte) (int, error) {
	if err := c.writeFrame(opBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *WSConn) readFrame() (byte, []byte, error) {
	var head [2]byte
	if _, err := io.ReadFull(c.reader, head[:]); err != nil {
		return 0, nil, err
	}
	opcode := head[0] & 0x0F
	masked := head[1]&0x80 != 0
	length := int(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.reader, ext[:]); err != nil {
			return 0, nil, err
		}
		v := binary.BigEndian.Uint64(ext[:])
		if v > maxFrameSize {
			return 0, nil, fmt.Errorf("%w: кадр %d байт превышает предел", ErrWSProtocol, v)
		}
		length = int(v)
	}
	if length < 0 || length > maxFrameSize {
		return 0, nil, fmt.Errorf("%w: недопустимая длина кадра %d", ErrWSProtocol, length)
	}
	// Сервер не обязан маскировать кадры, но для устойчивости поддерживаем оба случая.
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

func (c *WSConn) writeFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	head := make([]byte, 0, 14)
	head = append(head, 0x80|opcode) // FIN=1
	n := len(payload)
	switch {
	case n < 126:
		head = append(head, byte(n)|0x80) // MASK=1
	case n <= 0xFFFF:
		head = append(head, 126|0x80)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		head = append(head, ext[:]...)
	default:
		head = append(head, 127|0x80)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		head = append(head, ext[:]...)
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return fmt.Errorf("transport: генерация маски кадра: %w", err)
	}
	head = append(head, mask[:]...)

	masked := make([]byte, n)
	for i := 0; i < n; i++ {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := c.conn.Write(append(head, masked...)); err != nil {
		return err
	}
	return nil
}

// Close отправляет кадр закрытия и разрывает соединение.
func (c *WSConn) Close() error {
	_ = c.writeFrame(opClose, []byte{0x03, 0xE8}) // 1000, нормальное закрытие
	return c.conn.Close()
}

// LocalAddr возвращает локальный адрес соединения.
func (c *WSConn) LocalAddr() net.Addr { return c.conn.LocalAddr() }

// RemoteAddr возвращает удалённый адрес соединения.
func (c *WSConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// SetDeadline устанавливает крайний срок операций.
func (c *WSConn) SetDeadline(t time.Time) error { return c.conn.SetDeadline(t) }

// SetReadDeadline устанавливает крайний срок чтения.
func (c *WSConn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

// SetWriteDeadline устанавливает крайний срок записи.
func (c *WSConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
