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
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrFrameSize возвращается при некорректном заголовке кадра.
var ErrFrameSize = errors.New("mtproto: некорректный размер кадра")

// ErrFrameAlignment возвращается, если полезная нагрузка не выровнена
// по четырём байтам, что требуется протоколом.
var ErrFrameAlignment = errors.New("mtproto: полезная нагрузка не выровнена по 4 байта")

// MaxFrameSize ограничивает размер одного кадра: 16 МиБ данных плюс
// запас на заголовок и выравнивание.
const MaxFrameSize = 16 << 20

// Framing кодирует и разбирает кадры выбранного протокола кадрирования.
type Framing interface {
	// Encode оборачивает полезную нагрузку payload в кадр протокола.
	// payload должна быть выровнена по четырём байтам.
	Encode(dst []byte, payload []byte) ([]byte, error)
	// DecodeLength определяет полный размер кадра по его началу.
	// Возвращает ErrNeedMore, если данных пока недостаточно, и ErrFrameSize,
	// если заголовок некорректен.
	DecodeLength(header []byte) (int, error)
	// Decode извлекает полезную нагрузку кадра из буфера buf.
	// Возвращает срез, ссылающийся на buf, и число потреблённых байт.
	// Если данных недостаточно, возвращает ErrNeedMore.
	Decode(buf []byte) ([]byte, int, error)
	// HeaderLen возвращает число байт, необходимое для вычисления
	// DecodeLength при минимальном размере кадра.
	HeaderLen() int
}

// ErrNeedMore сигнализирует о необходимости дочитать данные.
var ErrNeedMore = errors.New("mtproto: требуется больше данных")

// Abridged — протокол кадрирования abridged (тег 0xEFEFEFEF).
//
// Длина кодируется в 32-битных словах одним байтом, либо четырьмя байтами
// (0x7F и младшие 24 бита длины), если длина не помещается в один байт.
type Abridged struct{}

// Encode реализует Framing.
func (Abridged) Encode(dst, payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: пустая полезная нагрузка", ErrFrameSize)
	}
	if len(payload)%4 != 0 {
		return nil, fmt.Errorf("%w: %d байт", ErrFrameAlignment, len(payload))
	}
	words := len(payload) / 4
	// Заголовок длины располагается перед полезной нагрузкой.
	if words < 0x7F {
		dst = append(dst[:0], byte(words))
		return append(dst, payload...), nil
	}
	if words > 0xFFFFFF {
		return nil, fmt.Errorf("%w: %d байт", ErrFrameSize, len(payload))
	}
	dst = append(dst[:0], 0x7F, byte(words), byte(words>>8), byte(words>>16))
	return append(dst, payload...), nil
}

// DecodeLength реализует Framing.
func (Abridged) DecodeLength(header []byte) (int, error) {
	if len(header) < 1 {
		return 0, ErrNeedMore
	}
	first := int8(header[0])
	if first == 0x7F {
		if len(header) < 4 {
			return 0, ErrNeedMore
		}
		words := int(header[1]) | int(header[2])<<8 | int(header[3])<<16
		if words < 0x7F {
			return 0, fmt.Errorf("%w: длина %d вне диапазона", ErrFrameSize, words)
		}
		return words<<2 + 4, nil
	}
	if first > 0 && first < 0x7F {
		return int(first)<<2 + 1, nil
	}
	return 0, fmt.Errorf("%w: байт длины 0x%02x", ErrFrameSize, header[0])
}

// HeaderLen реализует Framing.
func (Abridged) HeaderLen() int { return 4 }

// Decode реализует Framing.
func (a Abridged) Decode(buf []byte) ([]byte, int, error) {
	total, err := a.DecodeLength(buf)
	if err != nil {
		return nil, 0, err
	}
	if len(buf) < total {
		return nil, 0, ErrNeedMore
	}
	sizeLen := 1
	if buf[0] == 0x7F {
		sizeLen = 4
	}
	return buf[sizeLen:total], total, nil
}

// Intermediate — протокол кадрирования intermediate (тег 0xDDDDDDDD).
//
// Длина кодируется 32-битным числом байт без заголовка протокола, после
// которого добавляется от 0 до 15 случайных байт выравнивания.
type Intermediate struct {
	// Rand используется для заполнения выравнивания; поле оставлено
	// для возможности подмены источника случайных данных в тестах.
	Rand func(n int) ([]byte, error)
}

// Encode реализует Framing.
//
// Длина в протоколе intermediate измеряется в байтах, поэтому полезная
// нагрузка не обязана быть выровнена по четырём байтам: выравнивание,
// добавленное отправителем, является частью кадра.
func (f Intermediate) Encode(dst, payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: пустая полезная нагрузка", ErrFrameSize)
	}
	randf := f.Rand
	if randf == nil {
		randf = RandomBytes
	}
	pad, err := randf(4)
	if err != nil {
		return nil, err
	}
	padding := int(pad[0] & 0x0F)
	body := len(payload) + padding

	var head [4]byte
	binary.LittleEndian.PutUint32(head[:], uint32(body))
	dst = append(dst[:0], head[:]...)
	dst = append(dst, payload...)
	for i := 0; i < padding; i++ {
		dst = append(dst, pad[i%4])
	}
	return dst, nil
}

// DecodeLength реализует Framing.
func (Intermediate) DecodeLength(header []byte) (int, error) {
	if len(header) < 4 {
		return 0, ErrNeedMore
	}
	value := int(binary.LittleEndian.Uint32(header[:4])) + 4
	if value < 8 || value > MaxFrameSize {
		return 0, fmt.Errorf("%w: длина %d вне диапазона", ErrFrameSize, value)
	}
	return value, nil
}

// HeaderLen реализует Framing.
func (Intermediate) HeaderLen() int { return 4 }

// Decode реализует Framing.
func (i Intermediate) Decode(buf []byte) ([]byte, int, error) {
	total, err := i.DecodeLength(buf)
	if err != nil {
		return nil, 0, err
	}
	if len(buf) < total {
		return nil, 0, ErrNeedMore
	}
	// Полезная нагрузка включает случайное выравнивание, добавленное
	// при кодировании: сервер определяет длину сообщения по полю внутри
	// самого сообщения MTProto, поэтому хвост выравнивания безопасен.
	return buf[4:total], total, nil
}

// NewFraming возвращает реализацию Framing по тегу протокола.
func NewFraming(tag [4]byte) (Framing, error) {
	switch tag {
	case ProtoTagAbridged, ProtoTagSecure:
		return Abridged{}, nil
	case ProtoTagIntermediate:
		return Intermediate{}, nil
	default:
		return nil, fmt.Errorf("%w: тег %#08x", ErrBadHandshake, tag)
	}
}
