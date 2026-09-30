// Package testutil содержит эталонную реализацию стороны клиента MTProto.
//
// Она используется исключительно в тестах HumanGram-Proxy и повторяет
// действия TcpConnection из tdesktop: формирование 64-байтового префикса
// обфускации и вывод ключей. Совпадение поведения с этим кодом означает,
// что прокси совместим с настоящими клиентами Telegram.
//
// Данный файл распространяется по лицензии MIT.
// Copyright (c) 2026 HumanGram contributors
package testutil

import (
	"encoding/binary"
	"fmt"

	"github.com/HumanGram/humangram-proxy/internal/mtproto"
)

// Client повторяет состояние клиента Telegram на стороне прокси.
type Client struct {
	// Init — 64 байта, отправляемые в сокет.
	Init []byte

	// Send шифрует данные, отправляемые прокси.
	Send *mtproto.Stream
	// Recv расшифровывает данные, приходящие от прокси.
	Recv *mtproto.Stream

	// SendFraming — протокол кадрирования, объявленный клиентом.
	SendFraming mtproto.Framing
	// RecvFraming — протокол кадрирования для входящих данных.
	RecvFraming mtproto.Framing

	// tag — протокол кадрирования, объявленный клиентом.
	tag [4]byte
}

// NewClient строит клиента с заданным секретом, тегом протокола и индексом
// дата-центра. Возвращает клиента и 64-байтовый префикс для отправки.
func NewClient(secret []byte, tag [4]byte, dcIndex int16) (*Client, []byte, error) {
	var init [mtproto.HandshakeLen]byte
	for attempt := 0; attempt < 128; attempt++ {
		if _, err := mtproto.RandomBytes(len(init[:])); err != nil {
			return nil, nil, err
		}
		nonce, err := mtproto.RandomBytes(mtproto.HandshakeLen)
		if err != nil {
			return nil, nil, err
		}
		if !mtproto.GoodNonce(nonce) {
			continue
		}
		copy(init[:], nonce)

		sendKey, err := mtproto.DeriveKey(nonce[mtproto.SkipLen:mtproto.SkipLen+mtproto.KeyLen], secret)
		if err != nil {
			return nil, nil, err
		}
		sendIV := append([]byte(nil), nonce[mtproto.SkipLen+mtproto.KeyLen:mtproto.SkipLen+mtproto.KeyLen+mtproto.IVLen]...)

		// Клиент шифрует весь префикс, но в сокет отправляет первые
		// 56 байт открыто и последние восемь — зашифрованными.
		tail := make([]byte, 8)
		copy(tail[0:4], tag[:])
		binary.LittleEndian.PutUint16(tail[4:6], uint16(dcIndex))
		if _, err := mtproto.RandomBytes(2); err != nil {
			return nil, nil, err
		}
		extra, _ := mtproto.RandomBytes(2)
		copy(tail[6:8], extra)

		scratch, err := mtproto.NewStream(sendKey, sendIV)
		if err != nil {
			return nil, nil, err
		}
		encrypted := make([]byte, mtproto.HandshakeLen)
		scratch.XOR(encrypted, nonce)
		for i := range tail {
			tail[i] ^= encrypted[mtproto.ProtoTagPos+i] ^ nonce[mtproto.ProtoTagPos+i]
		}
		copy(init[mtproto.ProtoTagPos:], tail)

		// Состояние отправки продолжается с позиции 64 байта.
		enc, err := mtproto.NewStream(sendKey, sendIV)
		if err != nil {
			return nil, nil, err
		}
		if err := enc.Skip(mtproto.HandshakeLen); err != nil {
			return nil, nil, err
		}
		framing, err := mtproto.NewFraming(tag)
		if err != nil {
			return nil, nil, err
		}
		return &Client{
			Init:        append([]byte(nil), init[:]...),
			Send:        enc,
			SendFraming: framing,
			RecvFraming: framing,
			tag:         tag,
		}, append([]byte(nil), init[:]...), nil
	}
	return nil, nil, fmt.Errorf("testutil: не удалось построить клиента")
}

// AcceptHandshake обрабатывает ответ прокси и настраивает приём данных.
//
// Важно: ответ прокси клиенту не нужен. Клиент читает ровно 64 байта, но
// НЕ расходует их из генератора ключевого потока приёма: ответ сервера
// приходит открыто, а зашифрованный поток начинается с позиции 0.
// Именно так ведёт себя TcpConnection в tdesktop, где _receiveState
// используется впервые только при отправке данных клиенту.
//
// Состояние отправки, напротив, сдвинуто на 64 байта: клиент зашифровал
// весь префикс перед отправкой.
func (c *Client) AcceptHandshake(reply []byte, secret []byte) error {
	if len(reply) != mtproto.HandshakeLen {
		return fmt.Errorf("testutil: ответ прокси длиной %d байт", len(reply))
	}
	rev := mtproto.ReverseBytes(c.Init[mtproto.SkipLen : mtproto.SkipLen+mtproto.KeyLen+mtproto.IVLen])
	key, err := mtproto.DeriveKey(rev[:mtproto.KeyLen], secret)
	if err != nil {
		return err
	}
	recv, err := mtproto.NewStream(key, rev[mtproto.KeyLen:])
	if err != nil {
		return err
	}
	c.Recv = recv

	framing, err := mtproto.NewFraming(c.tag)
	if err != nil {
		return err
	}
	c.RecvFraming = framing
	return nil
}

// Tag возвращает протокол кадрирования, объявленный клиентом.
func (c *Client) Tag() [4]byte { return c.tag }
