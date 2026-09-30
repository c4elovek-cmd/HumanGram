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
	"encoding/binary"
	"testing"
)

// referenceClient повторяет действия TcpConnection на стороне клиента
// (см. prepareConnectionStartPrefix в tdesktop). Он нужен для проверки того,
// что прокси выводит те же ключи, что и настоящий клиент Telegram.
type referenceClient struct {
	nonce     []byte
	secret    []byte
	sendKey   []byte
	sendIV    []byte
	recvKey   []byte
	recvIV    []byte
	sendState *Stream
	recvState *Stream
}

// newReferenceClient строит состояние клиента и возвращает 64-байтовый
// префикс, который клиент отправляет в сокет.
func newReferenceClient(t *testing.T, secret []byte, protoTag [4]byte, dcIndex int16) (*referenceClient, []byte) {
	t.Helper()

	for attempt := 0; attempt < 128; attempt++ {
		nonce, err := RandomBytes(HandshakeLen)
		if err != nil {
			t.Fatalf("RandomBytes: %v", err)
		}
		if !GoodNonce(nonce) {
			continue
		}

		// Сохраняем исходный nonce: клиент не изменяет его на месте.
		nonce = append([]byte(nil), nonce...)

		sendKey, err := DeriveKey(nonce[SkipLen:SkipLen+KeyLen], secret)
		if err != nil {
			t.Fatalf("DeriveKey(send): %v", err)
		}
		// IV клиента — участок префикса [40:56].
		sendIV := append([]byte(nil), nonce[SkipLen+KeyLen:SkipLen+KeyLen+IVLen]...)

		rev := ReverseBytes(nonce[SkipLen : SkipLen+KeyLen+IVLen])
		recvKey, err := DeriveKey(rev[:KeyLen], secret)
		if err != nil {
			t.Fatalf("DeriveKey(recv): %v", err)
		}
		recvIV := append([]byte(nil), rev[KeyLen:]...)

		// Клиент пишет в сокет первые 56 байт открыто, затем — последние
		// восемь байт, зашифрованные потоком, уже сдвинутым на 64 байта.
		tail := make([]byte, 8)
		copy(tail[0:4], protoTag[:])
		binary.LittleEndian.PutUint16(tail[4:6], uint16(dcIndex))
		copy(tail[6:8], []byte{0x11, 0x22})

		sendState, err := NewStream(sendKey, sendIV)
		if err != nil {
			t.Fatalf("NewStream(send): %v", err)
		}
		encryptedFull := make([]byte, HandshakeLen)
		sendState.XOR(encryptedFull, nonce)
		for i := 0; i < 8; i++ {
			tail[i] ^= encryptedFull[ProtoTagPos+i] ^ nonce[ProtoTagPos+i]
		}

		// Состояние отправки продолжается с позиции 64.
		cont, err := NewStream(sendKey, sendIV)
		if err != nil {
			t.Fatalf("NewStream(send continue): %v", err)
		}
		if err := cont.Skip(HandshakeLen); err != nil {
			t.Fatalf("Skip: %v", err)
		}
		recvState, err := NewStream(recvKey, recvIV)
		if err != nil {
			t.Fatalf("NewStream(recv): %v", err)
		}

		wire := append([]byte(nil), nonce[0:ProtoTagPos]...)
		wire = append(wire, tail...)

		return &referenceClient{
			nonce:     nonce,
			secret:    secret,
			sendKey:   sendKey,
			sendIV:    sendIV,
			recvKey:   recvKey,
			recvIV:    recvIV,
			sendState: cont,
			recvState: recvState,
		}, wire
	}
	t.Fatal("не удалось построить состояние эталонного клиента")
	return nil, nil
}

func TestParseClientInitDetectsTagAndDC(t *testing.T) {
	secret := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}

	cases := []struct {
		name    string
		tag     [4]byte
		dcIndex int16
	}{
		{"abridged/dc1", ProtoTagAbridged, 1},
		{"abridged/dc2", ProtoTagAbridged, 2},
		{"abridged/media3", ProtoTagAbridged, -3},
		{"intermediate/dc4", ProtoTagIntermediate, 4},
		{"intermediate/media5", ProtoTagIntermediate, -5},
		{"secure/dc2", ProtoTagSecure, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, wire := newReferenceClient(t, secret, tc.tag, tc.dcIndex)

			got, err := ParseClientInit(wire, secret)
			if err != nil {
				t.Fatalf("ParseClientInit: %v", err)
			}
			if got.ProtoTag != tc.tag {
				t.Fatalf("тег протокола: получено %#08x, ожидалось %#08x", got.ProtoTag, tc.tag)
			}
			if got.DCIndex != tc.dcIndex {
				t.Fatalf("индекс ДЦ: получено %d, ожидалось %d", got.DCIndex, tc.dcIndex)
			}
			if got.DCID() != abs16(tc.dcIndex) {
				t.Fatalf("DCID: получено %d, ожидалось %d", got.DCID(), abs16(tc.dcIndex))
			}
			if got.IsMedia() != (tc.dcIndex < 0) {
				t.Fatalf("IsMedia: получено %v для индекса %d", got.IsMedia(), tc.dcIndex)
			}
		})
	}
}

func TestClientCryptoInterop(t *testing.T) {
	secret := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0x11, 0x22, 0x33, 0x44,
		0x55, 0x66, 0x77, 0x88, 0x99, 0x00, 0xff, 0xee}
	client, wire := newReferenceClient(t, secret, ProtoTagAbridged, 2)

	crypto, err := NewClientCrypto(wire, secret)
	if err != nil {
		t.Fatalf("NewClientCrypto: %v", err)
	}

	// Данные клиент -> прокси: клиент шифрует потоком, сдвинутым на 64 байта.
	plain := []byte("payload from telegram client to proxy")
	fromClient := make([]byte, len(plain))
	client.sendState.XOR(fromClient, plain)
	decrypted := make([]byte, len(fromClient))
	crypto.Decrypt.XOR(decrypted, fromClient)
	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("расшифровка клиент->прокси не совпала:\nполучено %q\nожидалось %q", decrypted, plain)
	}

	// Данные прокси -> клиент: обе стороны начинают с позиции 0.
	reply := []byte("payload from proxy back to telegram client")
	fromProxy := make([]byte, len(reply))
	crypto.Encrypt.XOR(fromProxy, reply)
	plainReply := make([]byte, len(fromProxy))
	client.recvState.XOR(plainReply, fromProxy)
	if !bytes.Equal(plainReply, reply) {
		t.Fatalf("расшифровка прокси->клиент не совпала:\nполучено %q\nожидалось %q", plainReply, reply)
	}
}

func TestParseClientInitRejectsForeignTraffic(t *testing.T) {
	// HTTP-запрос не должен приниматься за префикс сессии MTProto.
	http := []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n\x00\x00")
	_, err := ParseClientInit(http, nil)
	if err == nil {
		t.Fatal("ожидалась ошибка для HTTP-трафика")
	}
	if _, err := ParseClientInit(make([]byte, 10), nil); err == nil {
		t.Fatal("ожидалась ошибка для короткого префикса")
	}
}

func TestRelayInitShape(t *testing.T) {
	relay, err := NewRelayInit(ProtoTagAbridged, 2)
	if err != nil {
		t.Fatalf("NewRelayInit: %v", err)
	}
	if len(relay.Raw) != HandshakeLen {
		t.Fatalf("размер префикса: %d, ожидалось %d", len(relay.Raw), HandshakeLen)
	}
	if !GoodNonce(relay.Raw) {
		t.Fatal("сгенерированный префикс не проходит проверку GoodNonce")
	}
	// Хвост префикса должен читаться как протокол и индекс ДЦ.
	stream, err := NewStream(relay.EncKey, relay.EncIV)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	plain := make([]byte, HandshakeLen)
	stream.XOR(plain, relay.Raw)
	tag := [4]byte{plain[ProtoTagPos], plain[ProtoTagPos+1], plain[ProtoTagPos+2], plain[ProtoTagPos+3]}
	if tag != ProtoTagAbridged {
		t.Fatalf("тег протокола в префиксе: %#08x", tag)
	}
	if dc := int16(binary.LittleEndian.Uint16(plain[DCIndexPos : DCIndexPos+2])); dc != 2 {
		t.Fatalf("индекс ДЦ в префиксе: %d, ожидалось 2", dc)
	}
	// Ключ расшифровки — обратный порядок байтов участка [8:56].
	want := ReverseBytes(relay.Raw[SkipLen : SkipLen+KeyLen+IVLen])
	if !bytes.Equal(want[:KeyLen], relay.DecKey) {
		t.Fatal("ключ расшифровки не совпадает с обратным порядком байтов префикса")
	}
}

func abs16(v int16) int {
	if v < 0 {
		return int(-v)
	}
	return int(v)
}
