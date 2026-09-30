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
	"errors"
	"fmt"
)

// Размер 64-байтового префикса, которым начинается сессия обфускации.
const (
	// HandshakeLen — полный размер начального префикса сессии.
	HandshakeLen = 64
	// SkipLen — число ведущих байт префикса, не участвующих в обмене ключами.
	SkipLen = 8
	// ProtoTagPos — смещение 4-байтового тега протокола в префиксе.
	ProtoTagPos = 56
	// DCIndexPos — смещение 2-байтового индекса дата-центра в префиксе.
	DCIndexPos = 60
)

// Теги протокола кадрирования. Значения совпадают с TcpConnection::Protocol
// в tdesktop: 0xEFEFEFEF — abridged, 0xEEEEEEEE — secure, 0xDDDDDDDD —
// intermediate (padded).
var (
	// ProtoTagAbridged соответствует протоколу abridged (без шифрования
	// либо с шифрованием по секрету MTProxy).
	ProtoTagAbridged = [4]byte{0xEF, 0xEF, 0xEF, 0xEF}
	// ProtoTagSecure — зарезервированный тег защищённого транспорта.
	ProtoTagSecure = [4]byte{0xEE, 0xEE, 0xEE, 0xEE}
	// ProtoTagIntermediate соответствует протоколу intermediate (padded).
	ProtoTagIntermediate = [4]byte{0xDD, 0xDD, 0xDD, 0xDD}
)

// ErrBadHandshake возвращается, если префикс не распознан как сессия MTProto.
var ErrBadHandshake = errors.New("mtproto: некорректный префикс сессии")

// reservedStarts — значения первых четырёх байт, которые не должны встречаться
// в префиксе, чтобы вредоносный посредник не смог принять поток за HTTP,
// TLS или сам протокол MTProto.
var reservedStarts = [][4]byte{
	{0x48, 0x45, 0x41, 0x44}, // "HEAD"
	{0x50, 0x4F, 0x53, 0x54}, // "POST"
	{0x47, 0x45, 0x54, 0x20}, // "GET "
	{0x45, 0x45, 0x45, 0x45},
	{0x44, 0x44, 0x44, 0x44},
	{0x02, 0x01, 0x03, 0x16},
}

// GoodNonce сообщает, пригоден ли срез в качестве префикса сессии.
// Проверка идентична TcpSocket::isGoodStartNonce в tdesktop.
func GoodNonce(nonce []byte) bool {
	if len(nonce) < 2*4 {
		return false
	}
	// Первый байт 0xEF зарезервирован под маркер abridged-пакета.
	if nonce[0] == 0xEF {
		return false
	}
	first := binary.LittleEndian.Uint32(nonce[0:4])
	for _, bad := range reservedStarts {
		if first == binary.LittleEndian.Uint32(bad[:]) {
			return false
		}
	}
	// Второе слово не должно быть нулевым.
	return binary.LittleEndian.Uint32(nonce[4:8]) != 0
}

// ClientInit — разобранный 64-байтовый префикс, присланный клиентом.
type ClientInit struct {
	// Raw — префикс в том виде, в каком он пришёл из сокета.
	Raw []byte
	// ProtoTag — тег протокола кадрирования, объявленный клиентом.
	ProtoTag [4]byte
	// DCIndex — знаковый индекс дата-центра: положительный для основного
	// кластера, отрицательный для медиакластера.
	DCIndex int16
}

// DCID возвращает абсолютное значение индекса дата-центра.
func (c *ClientInit) DCID() int {
	if c.DCIndex < 0 {
		return int(-c.DCIndex)
	}
	return int(c.DCIndex)
}

// IsMedia сообщает, относится ли соединение к медиакластеру.
func (c *ClientInit) IsMedia() bool { return c.DCIndex < 0 }

// ParseClientInit разбирает префикс клиента и извлекает тег протокола
// и индекс дата-центра.
//
// Первые 56 байт префикса клиент передаёт открыто, последние 8 байт —
// зашифрованными. Поэтому для восстановления последних восьми байт
// расшифровывается весь префикс с позиции 0, а используются только байты
// начиная с ProtoTagPos.
func ParseClientInit(init, secret []byte) (*ClientInit, error) {
	if len(init) != HandshakeLen {
		return nil, fmt.Errorf("%w: %d байт, ожидалось %d", ErrBadHandshake, len(init), HandshakeLen)
	}
	prekey := init[SkipLen : SkipLen+KeyLen]
	iv := init[SkipLen+KeyLen : SkipLen+KeyLen+IVLen]

	key, err := DeriveKey(prekey, secret)
	if err != nil {
		return nil, err
	}
	stream, err := NewStream(key, iv)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, HandshakeLen)
	stream.XOR(plain, init)

	tag := [4]byte{plain[ProtoTagPos], plain[ProtoTagPos+1], plain[ProtoTagPos+2], plain[ProtoTagPos+3]}
	if tag != ProtoTagAbridged && tag != ProtoTagSecure && tag != ProtoTagIntermediate {
		return nil, fmt.Errorf("%w: неизвестный тег протокола %#08x", ErrBadHandshake, tag)
	}
	return &ClientInit{
		Raw:      append([]byte(nil), init...),
		ProtoTag: tag,
		DCIndex:  int16(binary.LittleEndian.Uint16(plain[DCIndexPos : DCIndexPos+2])),
	}, nil
}

// RelayInit — состояние восходящего канала «прокси -> дата-центр».
//
// Прокси самостоятельно генерирует 64-байтовый префикс для соединения
// с сервером Telegram и возвращает те же байты клиенту в качестве ответа,
// чтобы клиент корректно вывел обратные ключи.
type RelayInit struct {
	// Raw — префикс, отправляемый клиенту в качестве ответа.
	Raw []byte
	// EncKey, EncIV — ключ и вектор для шифрования данных к серверу Telegram.
	EncKey []byte
	EncIV  []byte
	// DecKey, DecIV — ключ и вектор для расшифровки данных от сервера Telegram.
	DecKey []byte
	DecIV  []byte
}

// NewRelayInit генерирует префикс восходящего канала.
//
// Префикс строится так же, как и у клиента: первые 56 байт — случайные и
// передаются открыто, последние 8 байт (тег протокола, индекс дата-центра
// и два случайных байта) — зашифрованы ключом, выведенным из самого префикса.
func NewRelayInit(protoTag [4]byte, dcIndex int16) (*RelayInit, error) {
	for attempt := 0; attempt < 128; attempt++ {
		rnd, err := RandomBytes(HandshakeLen)
		if err != nil {
			return nil, err
		}
		if !GoodNonce(rnd) {
			continue
		}
		relay, err := buildRelayInit(rnd, protoTag, dcIndex)
		if err != nil {
			return nil, err
		}
		return relay, nil
	}
	return nil, errors.New("mtproto: не удалось сгенерировать корректный префикс")
}

func buildRelayInit(rnd []byte, protoTag [4]byte, dcIndex int16) (*RelayInit, error) {
	encKey := rnd[SkipLen : SkipLen+KeyLen]
	encIV := rnd[SkipLen+KeyLen : SkipLen+KeyLen+IVLen]

	// Пропускаем весь префикс через генератор ключевого потока: так
	// восстанавливается keystream для последних восьми байт, и заодно
	// позиция потока выравнивается на 64 байта для последующих данных.
	stream, err := NewStream(encKey, encIV)
	if err != nil {
		return nil, err
	}
	streamed := make([]byte, HandshakeLen)
	stream.XOR(streamed, rnd)

	var keystream [8]byte
	for i := range keystream {
		pos := ProtoTagPos + i
		keystream[i] = streamed[pos] ^ rnd[pos]
	}

	tail := make([]byte, 8)
	copy(tail[0:4], protoTag[:])
	binary.LittleEndian.PutUint16(tail[4:6], uint16(dcIndex))
	extra, err := RandomBytes(2)
	if err != nil {
		return nil, err
	}
	copy(tail[6:8], extra)
	for i := range tail {
		tail[i] ^= keystream[i]
	}

	raw := append([]byte(nil), rnd...)
	copy(raw[ProtoTagPos:], tail)

	// Ключи расшифровки входящего потока — обратный порядок байтов
	// участка префикса, участвовавшего в обмене ключами.
	rev := ReverseBytes(rnd[SkipLen : SkipLen+KeyLen+IVLen])
	decKey := append([]byte(nil), rev[:KeyLen]...)
	decIV := append([]byte(nil), rev[KeyLen:]...)

	return &RelayInit{
		Raw:    raw,
		EncKey: append([]byte(nil), encKey...),
		EncIV:  append([]byte(nil), encIV...),
		DecKey: decKey,
		DecIV:  decIV,
	}, nil
}

// ClientCrypto — состояния шифрования для стороны «клиент <-> прокси».
type ClientCrypto struct {
	// Decrypt расшифровывает входящий от клиента поток. Позиция генератора
	// выровнена на 64 байта, то есть префикс уже учтён.
	Decrypt *Stream
	// Encrypt шифрует исходящий к клиенту поток.
	Encrypt *Stream
}

// Encrypted сообщает, требуется ли шифрование на стороне клиента.
func (c *ClientCrypto) Encrypted() bool {
	return c != nil && c.Decrypt != nil
}

// NewClientCrypto выводит состояния шифрования из префикса клиента.
//
// Ключ расшифровки выводится из участка префикса [8:56] напрямую, ключ
// шифрования — из тех же байт в обратном порядке. Оба ключа дополнительно
// хешируются SHA-256 вместе с секретом, если секрет задан.
func NewClientCrypto(init, secret []byte) (*ClientCrypto, error) {
	if len(init) != HandshakeLen {
		return nil, fmt.Errorf("%w: %d байт, ожидалось %d", ErrBadHandshake, len(init), HandshakeLen)
	}
	prekey := init[SkipLen : SkipLen+KeyLen]
	iv := init[SkipLen+KeyLen : SkipLen+KeyLen+IVLen]

	decKey, err := DeriveKey(prekey, secret)
	if err != nil {
		return nil, err
	}
	dec, err := NewStream(decKey, iv)
	if err != nil {
		return nil, err
	}
	// Клиент уже зашифровал префикс, поэтому выравниваем позицию потока.
	if err := dec.Skip(HandshakeLen); err != nil {
		return nil, err
	}

	rev := ReverseBytes(init[SkipLen : SkipLen+KeyLen+IVLen])
	encKey, err := DeriveKey(rev[:KeyLen], secret)
	if err != nil {
		return nil, err
	}
	enc, err := NewStream(encKey, rev[KeyLen:])
	if err != nil {
		return nil, err
	}
	return &ClientCrypto{Decrypt: dec, Encrypt: enc}, nil
}

// SameProtocolTag сообщает, совпадают ли теги протокола.
func SameProtocolTag(a, b [4]byte) bool { return bytes.Equal(a[:], b[:]) }
