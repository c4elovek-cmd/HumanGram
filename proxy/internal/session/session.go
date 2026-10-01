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

package session

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/HumanGram/humangram-proxy/internal/mtproto"
)

// UpstreamDialer устанавливает соединение с сервером Telegram.
//
// Параметр tag содержит протокол кадрирования, объявленный клиентом:
// тот же протокол используется и на стороне сервера.
type UpstreamDialer interface {
	Dial(ctx context.Context, dcID int, dcIndex int16, tag [4]byte) (net.Conn, error)
}

// Config настраивает одну сессию ретрансляции.
type Config struct {
	// Secret — 16-байтовый секрет в виде необработанных байт.
	// Пустой секрет означает режим abridged без шифрования.
	Secret []byte
	// Upstream устанавливает соединение с сервером Telegram.
	Upstream UpstreamDialer
	// Logger получает диагностические сообщения. При nil используется
	// slog.Default.
	Logger *slog.Logger
	// HandshakeTimeout ограничивает обмен префиксами с клиентом.
	HandshakeTimeout time.Duration
	// IdleTimeout закрывает сессию при отсутствии активности.
	// Нулевое или отрицательное значение отключает таймаут.
	IdleTimeout time.Duration
}

func (c *Config) withDefaults() {
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.HandshakeTimeout <= 0 {
		c.HandshakeTimeout = 15 * time.Second
	}
}

// Run обслуживает одну клиентскую сессию до её завершения.
func Run(ctx context.Context, client net.Conn, cfg Config) error {
	cfg.withDefaults()
	log := cfg.Logger.With("remote", client.RemoteAddr().String())

	// Клиент Telegram не отправляет данные до получения ответа на префикс,
	// поэтому ограничиваем время ожидания.
	_ = client.SetReadDeadline(time.Now().Add(cfg.HandshakeTimeout))
	init := make([]byte, mtproto.HandshakeLen)
	if _, err := io.ReadFull(client, init); err != nil {
		return fmt.Errorf("чтение префикса клиента: %w", err)
	}
	_ = client.SetReadDeadline(time.Time{})

	parsed, err := mtproto.ParseClientInit(init, cfg.Secret)
	if err != nil {
		return err
	}
	dcID, dcIndex := parsed.DCID(), parsed.DCIndex
	log = log.With(
		"dc", dcID,
		"media", parsed.IsMedia(),
		"proto", fmt.Sprintf("%#08x", parsed.ProtoTag),
	)
	log.Debug("префикс клиента разобран")

	// Ответ прокси строится на собственном префиксе: клиент выводит из него
	// ключи расшифровки нашего потока.
	relay, err := mtproto.NewRelayInit(parsed.ProtoTag, dcIndex)
	if err != nil {
		return err
	}
	if _, err := client.Write(relay.Raw); err != nil {
		return fmt.Errorf("отправка ответа на префикс: %w", err)
	}

	clientCrypto, err := mtproto.NewClientCrypto(init, cfg.Secret)
	if err != nil {
		return err
	}
	clientFraming, err := mtproto.NewFraming(parsed.ProtoTag)
	if err != nil {
		return err
	}
	upstreamFraming, err := mtproto.NewFraming(parsed.ProtoTag)
	if err != nil {
		return err
	}

	dialCtx, cancel := context.WithTimeout(ctx, cfg.HandshakeTimeout)
	defer cancel()
	upstream, err := cfg.Upstream.Dial(dialCtx, dcID, dcIndex, parsed.ProtoTag)
	if err != nil {
		return fmt.Errorf("подключение к дата-центру %d: %w", dcID, err)
	}
	defer upstream.Close()
	log.Debug("соединение с дата-центром установлено")

	// На стороне сервера обфускацию уже обеспечивает соединение,
	// возвращённое UpstreamDialer, поэтому дополнительное шифрование не нужно.
	fromClient := NewFramed(client, clientFraming, clientCrypto.Decrypt, clientCrypto.Encrypt)
	fromServer := NewFramed(upstream, upstreamFraming, nil, nil)

	var wg sync.WaitGroup
	wg.Add(2)
	errCh := make(chan error, 2)

	// Первая ошибка в любом направлении должна разорвать оба соединения:
	// иначе противоположное направление останется заблокированным
	// на чтении и сессия не завершится.
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = client.Close()
			_ = upstream.Close()
		})
	}

	pump := func(name string, src, dst *Framed) {
		defer wg.Done()
		for {
			if cfg.IdleTimeout > 0 {
				_ = src.conn.SetReadDeadline(time.Now().Add(cfg.IdleTimeout))
			}
			payload, err := src.ReadPayload()
			if err != nil {
				select {
				case errCh <- fmt.Errorf("%s: %w", name, err):
				default:
				}
				closeBoth()
				return
			}
			if err := dst.WritePayload(payload); err != nil {
				select {
				case errCh <- fmt.Errorf("%s: %w", name, err):
				default:
				}
				closeBoth()
				return
			}
		}
	}

	go pump("клиент", fromClient, fromServer)
	go pump("сервер", fromServer, fromClient)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		log.Debug("сессия прервана по контексту")
		closeBoth()
		<-done
		return ctx.Err()
	case <-done:
	}

	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

// readFull читает ровно len(buf) байт, обрабатывая частичные чтения.
