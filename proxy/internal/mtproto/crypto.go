// Package mtproto реализует низкоуровневые примитивы транспорта MTProto:
// обфускацию (obfuscated2) и кодирование кадров (abridged / intermediate).
//
// Спецификация: https://core.telegram.org/mtproto
// Референс-реализация клиента: TcpConnection::Protocol в tdesktop
// (Telegram/SourceFiles/mtproto/connection_tcp.cpp).
//
// Данный файл распространяется по лицензии MIT.
// Copyright (c) 2026 HumanGram contributors
package mtproto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// Длины, зафиксированные протоколом обфускации.
const (
	// KeyLen — длина AES-ключа обфускации.
	KeyLen = 32
	// IVLen — длина вектора инициализации AES-CTR.
	IVLen = 16
)

var (
	// ErrKeySize возвращается при неверной длине AES-ключа.
	ErrKeySize = errors.New("mtproto: неверная длина AES-ключа")
	// ErrIVSize возвращается при неверной длине вектора инициализации.
	ErrIVSize = errors.New("mtproto: неверная длина вектора инициализации")
)

// ReverseBytes возвращает новый срез с байтами в обратном порядке.
// Исходный срез не изменяется.
func ReverseBytes(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[len(b)-1-i] = b[i]
	}
	return out
}

// DeriveKey выводит 32-байтовый AES-ключ из 32-байтового prekey.
//
// При непустом secret выполняется дополнительное хеширование SHA-256 —
// это режим «obfuscated2 with secret», который клиент использует при
// подключении к MTProxy. При пустом secret ключ равен самому prekey —
// это режим прямого подключения к серверу Telegram.
func DeriveKey(prekey, secret []byte) ([]byte, error) {
	if len(prekey) != KeyLen {
		return nil, fmt.Errorf("%w: prekey %d байт, ожидалось %d", ErrKeySize, len(prekey), KeyLen)
	}
	if len(secret) == 0 {
		key := make([]byte, KeyLen)
		copy(key, prekey)
		return key, nil
	}
	sum := sha256.Sum256(append(append(make([]byte, 0, KeyLen+len(secret)), prekey...), secret...))
	return sum[:], nil
}

// Stream — обёртка над AES-256-CTR с возможностью «перемотки»
// позиции генератора ключевого потока.
//
// CTR является потоковым шифром, поэтому расшифровка выполняется той же
// операцией, что и шифрование, а пропуск n байт эквивалентен discard(n).
type Stream struct {
	ctr cipher.Stream
}

// NewStream создаёт поток AES-256-CTR по ключу key и вектору инициализации iv.
func NewStream(key, iv []byte) (*Stream, error) {
	if len(key) != KeyLen {
		return nil, fmt.Errorf("%w: %d байт, ожидалось %d", ErrKeySize, len(key), KeyLen)
	}
	if len(iv) != IVLen {
		return nil, fmt.Errorf("%w: %d байт, ожидалось %d", ErrIVSize, len(iv), IVLen)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("mtproto: создание AES: %w", err)
	}
	return &Stream{ctr: cipher.NewCTR(block, iv)}, nil
}

// XOR шифрует src в dst (dst может совпадать с src) и возвращает dst.
func (s *Stream) XOR(dst, src []byte) []byte {
	if s == nil || s.ctr == nil {
		// Поток не инициализирован (abridged-режим без шифрования).
		return append(dst[:0], src...)
	}
	s.ctr.XORKeyStream(dst, src)
	return dst
}

// Skip продвигает генератор ключевого потока на n байт, не обрабатывая данные.
// Используется для выравнивания состояния после обмена 64-байтовым префиксом.
func (s *Stream) Skip(n int) error {
	if s == nil || s.ctr == nil {
		return nil
	}
	if n < 0 {
		return errors.New("mtproto: отрицательное смещение")
	}
	const chunk = 4096
	buf := make([]byte, chunk)
	for n > 0 {
		step := n
		if step > chunk {
			step = chunk
		}
		s.ctr.XORKeyStream(buf[:step], buf[:step])
		n -= step
	}
	return nil
}

// RandomBytes возвращает n криптографически стойких случайных байт.
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, fmt.Errorf("mtproto: генерация случайных данных: %w", err)
	}
	return b, nil
}
