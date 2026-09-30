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

package proxy_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HumanGram/humangram-proxy"
	"github.com/HumanGram/humangram-proxy/internal/mtproto"
	"github.com/HumanGram/humangram-proxy/internal/testutil"
)

// testSecret — секрет, общий для клиента и прокси в тестах.
var testSecret = []byte{
	0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
	0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
}

// aligned дополняет строку пробелами до кратности четырём байтам:
// протоколы кадрирования MTProto требуют такого выравнивания.
func aligned(s string) []byte {
	b := []byte(s)
	for len(b)%4 != 0 {
		b = append(b, ' ')
	}
	return b
}

// fakeDC — эмулятор сервера Telegram.
//
// Как и настоящий сервер, он отвечает тем же протоколом кадрирования,
// который объявил клиент. В протоколе intermediate кадр дополняется
// случайным выравниванием, поэтому проверки в тестах опираются на
// префикс, а не на точное равенство.
type fakeDC struct {
	ln net.Listener
}

// startFakeDC поднимает эмулятор дата-центра с заданным протоколом
// кадрирования. Обработчик получает полезную нагрузку кадра и возвращает
// ответ либо nil.
func startFakeDC(t *testing.T, tag [4]byte, handler func([]byte) []byte) *fakeDC {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("прослушивание эмулятора ДЦ: %v", err)
	}
	framing, err := mtproto.NewFraming(tag)
	if err != nil {
		t.Fatalf("протокол кадрирования эмулятора: %v", err)
	}
	dc := &fakeDC{ln: ln}
	go dc.serve(t, framing, handler)
	t.Cleanup(func() { _ = ln.Close() })
	return dc
}

func (d *fakeDC) addr() string { return d.ln.Addr().String() }

func (d *fakeDC) serve(t *testing.T, framing mtproto.Framing, handler func([]byte) []byte) {
	for {
		conn, err := d.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			if err := d.handle(conn, framing, handler); err != nil && err != io.EOF {
				t.Logf("эмулятор ДЦ: %v", err)
			}
		}()
	}
}

func (d *fakeDC) handle(conn net.Conn, framing mtproto.Framing, handler func([]byte) []byte) error {
	// Сторона сервера: читаем префикс клиента и отвечаем своим.
	// Восходящий канал не использует секрет, поэтому ключи берутся
	// напрямую из префикса. Направление «сервер -> клиент» использует
	// перевёрнутый блок целиком (48 байт), а не перевёрнутые 32 байта:
	// именно так выводит ключи приёма TcpConnection в tdesktop.
	init := make([]byte, mtproto.HandshakeLen)
	if _, err := io.ReadFull(conn, init); err != nil {
		return err
	}
	rev := mtproto.ReverseBytes(init[mtproto.SkipLen : mtproto.SkipLen+mtproto.KeyLen+mtproto.IVLen])
	toClientKey := rev[:mtproto.KeyLen]
	toClientIV := rev[mtproto.KeyLen:]
	fromClientKey := init[mtproto.SkipLen : mtproto.SkipLen+mtproto.KeyLen]
	fromClientIV := init[mtproto.SkipLen+mtproto.KeyLen : mtproto.SkipLen+mtproto.KeyLen+mtproto.IVLen]

	// Ответ сервера: первые 56 байт открыты, последние восемь зашифрованы.
	// Клиент использует ответ только для выравнивания потока, поэтому
	// содержимое хвоста для него непрозрачно.
	reply, err := mtproto.RandomBytes(mtproto.HandshakeLen)
	if err != nil {
		return err
	}
	tail, err := mtproto.RandomBytes(8)
	if err != nil {
		return err
	}
	scratch, err := mtproto.NewStream(toClientKey, toClientIV)
	if err != nil {
		return err
	}
	encrypted := make([]byte, mtproto.HandshakeLen)
	scratch.XOR(encrypted, reply)
	for i := range tail {
		tail[i] ^= encrypted[mtproto.ProtoTagPos+i] ^ reply[mtproto.ProtoTagPos+i]
	}
	wire := append(append([]byte(nil), reply[:mtproto.ProtoTagPos]...), tail...)
	if _, err := conn.Write(wire); err != nil {
		return err
	}

	enc, err := mtproto.NewStream(toClientKey, toClientIV)
	if err != nil {
		return err
	}
	if err := enc.Skip(mtproto.HandshakeLen); err != nil {
		return err
	}
	dec, err := mtproto.NewStream(fromClientKey, fromClientIV)
	if err != nil {
		return err
	}
	if err := dec.Skip(mtproto.HandshakeLen); err != nil {
		return err
	}

	var buf []byte
	for {
		chunk := make([]byte, 32*1024)
		n, err := conn.Read(chunk)
		if n > 0 {
			dec.XOR(chunk[:n], chunk[:n])
			buf = append(buf, chunk[:n]...)
		}
		for {
			payload, consumed, derr := framing.Decode(buf)
			if derr != nil {
				break
			}
			buf = buf[consumed:]
			resp := handler(payload)
			if resp == nil {
				continue
			}
			frame, ferr := framing.Encode(nil, resp)
			if ferr != nil {
				return ferr
			}
			enc.XOR(frame, frame)
			if _, werr := conn.Write(frame); werr != nil {
				return werr
			}
		}
		if err != nil {
			return err
		}
	}
}

// startProxy поднимает HumanGram-Proxy, направленный на адрес dcAddr.
func startProxy(t *testing.T, dcAddr string) *humangram.Proxy {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("HUMANGRAM_TEST_DEBUG") != "" {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	p, err := humangram.Start(context.Background(), humangram.Config{
		Secret:      fmt.Sprintf("%x", testSecret),
		Upstream:    humangram.UpstreamDirect,
		Logger:      logger,
		DCOverrides: map[string]string{"2": dcAddr},
		IdleTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("запуск прокси: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	return p
}

// dialClient подключается к прокси и выполняет обмен префиксами.
func dialClient(t *testing.T, addr string, tag [4]byte, dcIndex int16) (net.Conn, *testutil.Client) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("подключение к прокси: %v", err)
	}
	client, init, err := testutil.NewClient(testSecret, tag, dcIndex)
	if err != nil {
		t.Fatalf("построение клиента: %v", err)
	}
	if _, err := conn.Write(init); err != nil {
		t.Fatalf("отправка префикса: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reply := make([]byte, mtproto.HandshakeLen)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("чтение ответа прокси: %v", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	if err := client.AcceptHandshake(reply, testSecret); err != nil {
		t.Fatalf("обработка ответа прокси: %v", err)
	}
	return conn, client
}

// writeFrame отправляет кадр от имени клиента.
func writeFrame(t *testing.T, conn net.Conn, client *testutil.Client, payload []byte) {
	t.Helper()
	frame, err := client.SendFraming.Encode(nil, payload)
	if err != nil {
		t.Fatalf("кодирование кадра: %v", err)
	}
	client.Send.XOR(frame, frame)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("отправка кадра: %v", err)
	}
}

// readFrame читает кадр от имени клиента.
func readFrame(t *testing.T, conn net.Conn, client *testutil.Client) []byte {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	framing := client.RecvFraming
	var buf []byte
	for {
		if payload, consumed, err := framing.Decode(buf); err == nil {
			buf = buf[consumed:]
			out := make([]byte, len(payload))
			copy(out, payload)
			return out
		}
		chunk := make([]byte, 32*1024)
		n, err := conn.Read(chunk)
		if n > 0 {
			client.Recv.XOR(chunk[:n], chunk[:n])
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			t.Fatalf("чтение кадра: %v", err)
		}
	}
}

func TestProxyRelaysMTProtoBothDirections(t *testing.T) {
	tag := mtproto.ProtoTagIntermediate
	payload := aligned("req_pq_multi с тестовым содержимым")
	dcServer := startFakeDC(t, tag, func(got []byte) []byte {
		if !bytes.HasPrefix(got, payload) {
			return aligned(fmt.Sprintf("MISMATCH:%q", got))
		}
		return aligned("resPQ: ответ эмулятора")
	})
	p := startProxy(t, dcServer.addr())

	conn, client := dialClient(t, p.Addr(), tag, 2)
	defer conn.Close()

	writeFrame(t, conn, client, payload)
	got := readFrame(t, conn, client)
	if !bytes.HasPrefix(got, aligned("resPQ: ответ эмулятора")) {
		t.Fatalf("получено %q, ожидался корректный ответ", got)
	}
}

func TestProxySupportsAllProtocolTags(t *testing.T) {
	tags := []struct {
		name string
		tag  [4]byte
	}{
		{"abridged", mtproto.ProtoTagAbridged},
		{"secure", mtproto.ProtoTagSecure},
		{"intermediate", mtproto.ProtoTagIntermediate},
	}
	for _, tc := range tags {
		t.Run(tc.name, func(t *testing.T) {
			payload := aligned("пакет для " + tc.name)
			dcServer := startFakeDC(t, tc.tag, func(got []byte) []byte {
				return append([]byte("echo"), got...)
			})
			p := startProxy(t, dcServer.addr())

			conn, client := dialClient(t, p.Addr(), tc.tag, 2)
			defer conn.Close()

			writeFrame(t, conn, client, payload)
			got := readFrame(t, conn, client)
			want := append([]byte("echo"), payload...)
			if !bytes.HasPrefix(got, want) {
				t.Fatalf("получено %q, ожидалось начало %q", got, want)
			}
		})
	}
}

func TestProxyRejectsUnknownDC(t *testing.T) {
	// Прокси настроен на второй дата-центр; клиент сообщил третий.
	dcServer := startFakeDC(t, mtproto.ProtoTagAbridged, func(got []byte) []byte { return got })
	p := startProxy(t, dcServer.addr())

	conn, err := net.DialTimeout("tcp", p.Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("подключение: %v", err)
	}
	defer conn.Close()
	_, init, err := testutil.NewClient(testSecret, mtproto.ProtoTagAbridged, 3)
	if err != nil {
		t.Fatalf("построение клиента: %v", err)
	}
	if _, err := conn.Write(init); err != nil {
		t.Fatalf("отправка префикса: %v", err)
	}
	// Прокси отвечает префиксом, затем не может подключиться и закрывает
	// соединение.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	reply := make([]byte, mtproto.HandshakeLen)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("чтение ответа прокси: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("ожидалось закрытие соединения для неизвестного дата-центра")
	}
}

func TestProxyRelaysLargePayload(t *testing.T) {
	// Полезная нагрузка больше порции чтения проверяет сборку кадра
	// из нескольких фрагментов и корректность сброса буфера.
	tag := mtproto.ProtoTagIntermediate
	payload := make([]byte, 512*1024)
	for i := range payload {
		payload[i] = byte(i * 31)
	}
	dcServer := startFakeDC(t, tag, func(got []byte) []byte {
		return aligned(fmt.Sprintf("ok:%d", len(got)))
	})
	p := startProxy(t, dcServer.addr())

	conn, client := dialClient(t, p.Addr(), tag, 2)
	defer conn.Close()

	writeFrame(t, conn, client, payload)
	got := readFrame(t, conn, client)
	// Эмулятор сообщает длину принятого кадра. К сообщению добавляется
	// выравнивание дважды: один раз при кодировании клиентом, второй —
	// при повторном кодировании прокси. Каждое выравнивание не превышает
	// 15 байт.
	var reported int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(got)), "ok:%d", &reported); err != nil {
		t.Fatalf("получено %q, ожидалось подтверждение вида ok:N", got)
	}
	const maxPadding = 2 * 15
	if reported < len(payload) || reported > len(payload)+maxPadding {
		t.Fatalf("эмулятор принял %d байт, ожидалось от %d до %d",
			reported, len(payload), len(payload)+maxPadding)
	}
}

func TestProxyRelaysConsecutiveFrames(t *testing.T) {
	tag := mtproto.ProtoTagIntermediate
	var mu sync.Mutex
	var seen []int
	dcServer := startFakeDC(t, tag, func(got []byte) []byte {
		mu.Lock()
		seen = append(seen, len(got))
		mu.Unlock()
		return append([]byte("ack:"), got...)
	})
	p := startProxy(t, dcServer.addr())

	conn, client := dialClient(t, p.Addr(), tag, 2)
	defer conn.Close()

	const count = 5
	for i := 0; i < count; i++ {
		msg := aligned(fmt.Sprintf("сообщение-%d", i))
		writeFrame(t, conn, client, msg)
		got := readFrame(t, conn, client)
		if !bytes.HasPrefix(got, append([]byte("ack:"), msg...)) {
			t.Fatalf("кадр %d: получено %q", i, got)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != count {
		t.Fatalf("эмулятор ДЦ получил %d кадров, ожидалось %d", len(seen), count)
	}
}

func TestProxyRejectsNonMTProtoTraffic(t *testing.T) {
	dcServer := startFakeDC(t, mtproto.ProtoTagAbridged, func(got []byte) []byte { return got })
	p := startProxy(t, dcServer.addr())

	conn, err := net.DialTimeout("tcp", p.Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("подключение: %v", err)
	}
	defer conn.Close()

	// HTTP-запрос не является сессией MTProto и должен быть отвергнут.
	httpReq := "GET / HTTP/1.1\r\nHost: example.org\r\n\r\n" + strings.Repeat("\x00", 8)
	if _, err := conn.Write([]byte(httpReq)); err != nil {
		t.Fatalf("отправка HTTP: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 16)); err == nil {
		t.Fatal("ожидалось закрытие соединения для HTTP-трафика")
	}
}

func TestProxyHonoursFreePort(t *testing.T) {
	dcServer := startFakeDC(t, mtproto.ProtoTagAbridged, func(got []byte) []byte { return got })
	p := startProxy(t, dcServer.addr())
	if p.Port() <= 0 {
		t.Fatalf("порт %d не назначен", p.Port())
	}
	if !strings.HasPrefix(p.Addr(), "127.0.0.1:") {
		t.Fatalf("адрес %q не является локальным", p.Addr())
	}
	if p.Host() != "127.0.0.1" {
		t.Fatalf("хост %q, ожидался 127.0.0.1", p.Host())
	}
}

func TestProxyStopIsIdempotent(t *testing.T) {
	dcServer := startFakeDC(t, mtproto.ProtoTagAbridged, func(got []byte) []byte { return got })
	p := startProxy(t, dcServer.addr())
	conn, client := dialClient(t, p.Addr(), mtproto.ProtoTagIntermediate, 2)
	defer conn.Close()
	writeFrame(t, conn, client, aligned("проверка остановки"))

	// Остановка не должна блокироваться даже при активной сессии.
	done := make(chan error, 1)
	go func() { done <- p.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("остановка прокси: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("остановка прокси заблокирована")
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("повторная остановка: %v", err)
	}
}

