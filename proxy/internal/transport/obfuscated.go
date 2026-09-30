// Package transport реализует варианты восходящего транспорта HumanGram:
// прямое подключение с обфускацией и туннель MTProto поверх WebSocket.
//
// Данный файл распространяется по лицензии MIT.
// Copyright (c) 2026 HumanGram contributors
package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/HumanGram/humangram-proxy/internal/mtproto"
)

// ObfuscatedConn — соединение с сервером Telegram поверх обфускации
// (obfuscated2). Поверх него сессия передаёт кадры MTProto выбранного
// протокола кадрирования.
type ObfuscatedConn struct {
	conn net.Conn
	enc  *mtproto.Stream
	dec  *mtproto.Stream

	rbuf []byte
	wbuf []byte
}

// Handshake выполняет обмен 64-байтовыми префиксами обфускации поверх
// готового соединения и возвращает соединение, прозрачное для данных.
//
// Ранее установленное соединение conn передаётся вызывающей стороне в
// состав Upstream; здесь оно используется только для рукопожатия.
func Handshake(conn net.Conn, relay *mtproto.RelayInit) (net.Conn, error) {
	enc, err := mtproto.NewStream(relay.EncKey, relay.EncIV)
	if err != nil {
		return nil, err
	}
	// Отправленный префикс уже израсходовал 64 байта ключевого потока.
	if err := enc.Skip(mtproto.HandshakeLen); err != nil {
		return nil, err
	}
	if _, err := conn.Write(relay.Raw); err != nil {
		return nil, fmt.Errorf("отправка префикса обфускации: %w", err)
	}

	// Сервер отвечает собственным 64-байтовым префиксом.
	reply := make([]byte, mtproto.HandshakeLen)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return nil, fmt.Errorf("чтение ответа обфускации: %w", err)
	}

	dec, err := mtproto.NewStream(relay.DecKey, relay.DecIV)
	if err != nil {
		return nil, err
	}
	if err := dec.Skip(mtproto.HandshakeLen); err != nil {
		return nil, err
	}
	return &ObfuscatedConn{
		conn: conn,
		enc:  enc,
		dec:  dec,
		rbuf: make([]byte, 0, 64*1024),
		wbuf: make([]byte, 0, 64*1024),
	}, nil
}

// DialObfuscated устанавливает соединение с сервером Telegram и выполняет
// обмен 64-байтовыми префиксами обфускации.
//
// dcIndex записывается в префикс и используется сервером для выбора
// кластера; обычно здесь передаётся индекс, сообщённый клиентом.
func DialObfuscated(
	ctx context.Context,
	dialer *net.Dialer,
	network, addr string,
	tag [4]byte,
	dcIndex int16,
) (*ObfuscatedConn, error) {
	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("подключение к %s: %w", addr, err)
	}
	relay, err := mtproto.NewRelayInit(tag, dcIndex)
	if err != nil {
		conn.Close()
		return nil, err
	}
	deadline := time.Now().Add(15 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	result, err := Handshake(conn, relay)
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return result.(*ObfuscatedConn), nil
}

// Read реализует net.Conn: считывает и расшифровывает данные.
func (c *ObfuscatedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n, err := c.conn.Read(p)
	if n > 0 {
		c.dec.XOR(p[:n], p[:n])
	}
	return n, err
}

// Write реализует net.Conn: шифрует и отправляет данные.
func (c *ObfuscatedConn) Write(p []byte) (int, error) {
	buf := c.wbuf[:0]
	buf = append(buf, p...)
	c.enc.XOR(buf, buf)
	c.wbuf = buf
	n, err := c.conn.Write(buf)
	c.wbuf = buf[:0]
	return n, err
}

// Close закрывает соединение.
func (c *ObfuscatedConn) Close() error { return c.conn.Close() }

// LocalAddr возвращает локальный адрес соединения.
func (c *ObfuscatedConn) LocalAddr() net.Addr { return c.conn.LocalAddr() }

// RemoteAddr возвращает удалённый адрес соединения.
func (c *ObfuscatedConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// SetDeadline устанавливает крайний срок операций.
func (c *ObfuscatedConn) SetDeadline(t time.Time) error { return c.conn.SetDeadline(t) }

// SetReadDeadline устанавливает крайний срок чтения.
func (c *ObfuscatedConn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

// SetWriteDeadline устанавливает крайний срок записи.
func (c *ObfuscatedConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
