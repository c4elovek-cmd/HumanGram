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

package mtproto

import (
	"bytes"
	"errors"
	"testing"
)

func TestAbridgedRoundTrip(t *testing.T) {
	framing := Abridged{}
	sizes := []int{4, 16, 124, 500, 1024, 64 * 1024, 1 << 20}
	for _, size := range sizes {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i * 7)
		}
		frame, err := framing.Encode(nil, payload)
		if err != nil {
			t.Fatalf("Encode(%d): %v", size, err)
		}
		got, err := framing.DecodeLength(frame)
		if err != nil {
			t.Fatalf("DecodeLength(%d): %v", size, err)
		}
		if got != len(frame) {
			t.Fatalf("размер кадра для %d: получено %d, ожидалось %d", size, got, len(frame))
		}
		// Полезная нагрузка начинается сразу после заголовка длины.
		hdr := len(frame) - size
		if !bytes.Equal(frame[hdr:], payload) {
			t.Fatalf("полезная нагрузка не совпала для размера %d", size)
		}
	}
}

func TestAbridgedLongFormHeader(t *testing.T) {
	framing := Abridged{}
	// 0x7F и выше требуют четырёхбайтового заголовка.
	payload := make([]byte, 0x7F*4)
	frame, err := framing.Encode(nil, payload)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if frame[0] != 0x7F {
		t.Fatalf("ожидался маркер длинного заголовка 0x7F, получено 0x%02x", frame[0])
	}
	got, err := framing.DecodeLength(frame)
	if err != nil || got != len(frame) {
		t.Fatalf("DecodeLength: получено %d/%v, ожидалось %d", got, err, len(frame))
	}
}

func TestAbridgedRejectsInvalid(t *testing.T) {
	framing := Abridged{}
	for _, b := range [][]byte{{0x00}, {0x80}, {0xFF}, {0x7F, 0x01, 0x00, 0x00}} {
		if _, err := framing.DecodeLength(b); err == nil {
			t.Fatalf("ожидалась ошибка для байтов %#v", b)
		}
	}
	if _, err := framing.DecodeLength(nil); !errors.Is(err, ErrNeedMore) {
		t.Fatal("ожидалась ErrNeedMore для пустого буфера")
	}
	if _, err := framing.Encode(nil, []byte{1, 2, 3}); !errors.Is(err, ErrFrameAlignment) {
		t.Fatal("ожидалась ErrFrameAlignment для невыровненной нагрузки")
	}
}

func TestIntermediateRoundTrip(t *testing.T) {
	// Детерминированное заполнение выравнивания для проверки.
	framing := Intermediate{Rand: func(n int) ([]byte, error) {
		b := make([]byte, n)
		for i := range b {
			b[i] = 0xA0
		}
		return b, nil
	}}
	for _, size := range []int{4, 64, 4096, 1 << 20} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i)
		}
		frame, err := framing.Encode(nil, payload)
		if err != nil {
			t.Fatalf("Encode(%d): %v", size, err)
		}
		if len(frame) < 4 {
			t.Fatalf("кадр слишком короткий: %d", len(frame))
		}
		got, err := framing.DecodeLength(frame)
		if err != nil {
			t.Fatalf("DecodeLength(%d): %v", size, err)
		}
		if got != len(frame) {
			t.Fatalf("размер кадра для %d: получено %d, ожидалось %d", size, got, len(frame))
		}
		if !bytes.Equal(frame[4:4+size], payload) {
			t.Fatalf("полезная нагрузка не совпала для размера %d", size)
		}
	}
}

func TestIntermediatePaddingIsRandom(t *testing.T) {
	sizes := map[int]bool{}
	for i := 0; i < 200; i++ {
		framing := Intermediate{}
		frame, err := framing.Encode(nil, make([]byte, 16))
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		sizes[len(frame)] = true
	}
	if len(sizes) < 10 {
		t.Fatalf("выравнивание не выглядит случайным: %d различных размеров", len(sizes))
	}
}

func TestIntermediateRejectsInvalid(t *testing.T) {
	framing := Intermediate{}
	if _, err := framing.DecodeLength([]byte{0x01, 0x02}); !errors.Is(err, ErrNeedMore) {
		t.Fatal("ожидалась ErrNeedMore")
	}
	if _, err := framing.DecodeLength([]byte{0xFF, 0xFF, 0xFF, 0xFF}); err == nil {
		t.Fatal("ожидалась ошибка для слишком большой длины")
	}
}

func TestNewFramingByTag(t *testing.T) {
	if _, err := NewFraming(ProtoTagAbridged); err != nil {
		t.Fatalf("abridged: %v", err)
	}
	if _, err := NewFraming(ProtoTagSecure); err != nil {
		t.Fatalf("secure: %v", err)
	}
	if _, err := NewFraming(ProtoTagIntermediate); err != nil {
		t.Fatalf("intermediate: %v", err)
	}
	if _, err := NewFraming([4]byte{1, 2, 3, 4}); err == nil {
		t.Fatal("ожидалась ошибка для неизвестного тега")
	}
}

func TestDeriveKey(t *testing.T) {
	prekey := make([]byte, KeyLen)
	for i := range prekey {
		prekey[i] = byte(i)
	}
	// Без секрета ключ равен самому prekey.
	k, err := DeriveKey(prekey, nil)
	if err != nil || !bytes.Equal(k, prekey) {
		t.Fatalf("DeriveKey без секрета: %v %v", k, err)
	}
	// С секретом ключ отличается и остаётся детерминированным.
	k1, _ := DeriveKey(prekey, []byte("secret"))
	k2, _ := DeriveKey(prekey, []byte("secret"))
	if !bytes.Equal(k1, k2) {
		t.Fatal("DeriveKey не детерминирован")
	}
	if bytes.Equal(k1, prekey) {
		t.Fatal("DeriveKey с секретом должен отличаться от prekey")
	}
	if _, err := DeriveKey(prekey[:10], nil); !errors.Is(err, ErrKeySize) {
		t.Fatal("ожидалась ErrKeySize")
	}
}

func TestStreamSkipMatchesConsumedKeystream(t *testing.T) {
	key := make([]byte, KeyLen)
	iv := make([]byte, IVLen)
	for i := range key {
		key[i] = byte(i + 1)
	}
	for i := range iv {
		iv[i] = byte(0xF0 + i)
	}
	direct, err := NewStream(key, iv)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	consumed := make([]byte, 100)
	direct.XOR(consumed, consumed)

	skipped, err := NewStream(key, iv)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if err := skipped.Skip(100); err != nil {
		t.Fatalf("Skip: %v", err)
	}
	after := make([]byte, 16)
	direct.XOR(after, after)
	skipped.XOR(after, after)
	if !bytes.Equal(after, make([]byte, 16)) {
		t.Fatal("потоки разошлись после Skip")
	}
}

func TestReverseBytes(t *testing.T) {
	in := []byte{1, 2, 3, 4}
	out := ReverseBytes(in)
	if !bytes.Equal(out, []byte{4, 3, 2, 1}) {
		t.Fatalf("ReverseBytes: %v", out)
	}
	if !bytes.Equal(in, []byte{1, 2, 3, 4}) {
		t.Fatal("ReverseBytes изменил исходный срез")
	}
}
