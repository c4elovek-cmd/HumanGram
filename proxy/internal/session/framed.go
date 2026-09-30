// Package session реализует ретрансляцию трафика MTProto между локальным
// клиентом и сервером Telegram.
//
// Прокси работает прозрачно: он снимает обфускацию и кадрирование на
// стороне клиента и заново применяет их на стороне сервера, заново
// формируя заполнение и размеры кадров. Ключ авторизации клиент
// устанавливает самостоятельно и никогда не передаёт прокси, поэтому
// прокси не может читать содержимое переписки.
//
// Данный файл распространяется по лицензии MIT.
// Copyright (c) 2026 HumanGram contributors
package session

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/HumanGram/humangram-proxy/internal/mtproto"
)

// chunkSize — размер порции чтения из сокета.
const chunkSize = 32 * 1024

// ErrFrameTooLarge возвращается, если кадр превышает допустимый размер.
var ErrFrameTooLarge = errors.New("session: размер кадра превышает допустимый")

// Framed читает и записывает кадры MTProto поверх соединения, применяя
// обфускацию и выбранный протокол кадрирования.
type Framed struct {
	conn    net.Conn
	framing mtproto.Framing
	dec     *mtproto.Stream
	enc     *mtproto.Stream

	mu   sync.Mutex
	rbuf []byte
	wbuf []byte
	// scratch используется для выравнивания полезной нагрузки.
	scratch []byte
}

// NewFramed создаёт обёртку над соединением conn.
//
// Параметры dec и enc могут быть nil: в режиме abridged без секрета
// прокси данные не шифрует.
func NewFramed(conn net.Conn, framing mtproto.Framing, dec, enc *mtproto.Stream) *Framed {
	return &Framed{
		conn:    conn,
		framing: framing,
		dec:     dec,
		enc:     enc,
		rbuf:    make([]byte, 0, chunkSize),
		wbuf:    make([]byte, 0, chunkSize),
	}
}

// ReadPayload возвращает полезную нагрузку очередного кадра.
func (f *Framed) ReadPayload() ([]byte, error) {
	for {
		if payload, consumed, err := f.framing.Decode(f.rbuf); err == nil {
			// Копируем, так как буфер будет переиспользован при следующем чтении.
			out := make([]byte, len(payload))
			copy(out, payload)
			f.rbuf = f.rbuf[consumed:]
			return out, nil
		} else if !errors.Is(err, mtproto.ErrNeedMore) {
			return nil, fmt.Errorf("session: разбор кадра: %w", err)
		}

		chunk := make([]byte, chunkSize)
		n, err := f.conn.Read(chunk)
		if n > 0 {
			f.dec.XOR(chunk[:n], chunk[:n])
			f.rbuf = append(f.rbuf, chunk[:n]...)
			if len(f.rbuf) > mtproto.MaxFrameSize*2 {
				return nil, fmt.Errorf("%w: буфер %d байт", ErrFrameTooLarge, len(f.rbuf))
			}
		}
		if err != nil {
			if len(f.rbuf) > 0 && n == 0 && errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("session: соединение закрыто с неполным кадром (%d байт в буфере)", len(f.rbuf))
			}
			return nil, err
		}
	}
}

// WritePayload формирует и отправляет кадр с заданной полезной нагрузкой.
//
// Полезная нагрузка выравнивается по четырём байтам: протокол abridged
// измеряет длину в 32-битных словах, а протокол intermediate добавляет к
// кадру собственное выравнивание. Само сообщение MTProto всегда выровнено
// по четырём байтам, поэтому добавляемые нули не меняют его содержимого и
// безопасно игнорируются получателем.
func (f *Framed) WritePayload(payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if pad := len(payload) % 4; pad != 0 {
		f.scratch = append(f.scratch[:0], payload...)
		target := len(f.scratch) + (4 - pad)
		for len(f.scratch) < target {
			f.scratch = append(f.scratch, 0)
		}
		payload = f.scratch
	}
	frame, err := f.framing.Encode(f.wbuf[:0], payload)
	if err != nil {
		return fmt.Errorf("session: кодирование кадра: %w", err)
	}
	f.wbuf = frame
	f.enc.XOR(f.wbuf, f.wbuf)
	_, err = f.conn.Write(f.wbuf)
	return err
}

// Close закрывает соединение.
func (f *Framed) Close() error { return f.conn.Close() }
