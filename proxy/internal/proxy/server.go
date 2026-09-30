// Package proxy реализует локальный MTProto-прокси HumanGram.
//
// Данный файл распространяется по лицензии MIT.
// Copyright (c) 2026 HumanGram contributors
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HumanGram/humangram-proxy/internal/session"
)

// Config настраивает локальный прокси.
type Config struct {
	// Host — адрес прослушивания. По умолчанию 127.0.0.1: прокси
	// намеренно не выставляется во внешнюю сеть.
	Host string
	// Port — порт прослушивания. Ноль означает выбор свободного порта
	// операционной системой.
	Port int
	// Secret — секрет в виде 16 необработанных байт. Клиент должен быть
	// настроен на прокси с тем же секретом.
	Secret []byte
	// Upstream устанавливает соединения с сервером Telegram.
	Upstream session.UpstreamDialer
	// Logger получает диагностические сообщения.
	Logger *slog.Logger
	// HandshakeTimeout ограничивает обмен префиксами.
	HandshakeTimeout time.Duration
	// IdleTimeout закрывает сессию при отсутствии активности.
	IdleTimeout time.Duration
	// SessionHook вызывается при завершении каждой сессии. Предназначен
	// для диагностики и тестирования.
	SessionHook func(err error)
	// Parent — родительский контекст. Его отмена останавливает прокси.
	// При nil используется context.Background.
	Parent context.Context
}

func (c *Config) withDefaults() error {
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.Upstream == nil {
		return errors.New("proxy: не задан восходящий транспорт")
	}
	if len(c.Secret) != 0 && len(c.Secret) != 16 {
		return fmt.Errorf("proxy: секрет должен содержать 16 байт, получено %d", len(c.Secret))
	}
	return nil
}

// Server — локальный прокси MTProto.
type Server struct {
	cfg     Config
	log     *slog.Logger
	ln      net.Listener
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	closing atomic.Bool

	sessions atomic.Int64
}

// New создаёт прокси, но не начинает прослушивание.
func New(cfg Config) (*Server, error) {
	if err := cfg.withDefaults(); err != nil {
		return nil, err
	}
	parent := cfg.Parent
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Server{cfg: cfg, log: cfg.Logger, ctx: ctx, cancel: cancel}, nil
}

// Start открывает сокет прослушивания.
func (s *Server) Start() error {
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("proxy: прослушивание %s: %w", addr, err)
	}
	s.ln = ln
	s.log.Info("HumanGram: локальный MTProto-прокси запущен", "addr", ln.Addr().String())
	return nil
}

// Addr возвращает адрес прослушивания. До вызова Start возвращает nil.
func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Port возвращает номер порта прослушивания. До вызова Start возвращает 0.
func (s *Server) Port() int {
	if s.ln == nil {
		return 0
	}
	if tcp, ok := s.ln.Addr().(*net.TCPAddr); ok {
		return tcp.Port
	}
	return 0
}

// Sessions возвращает число активных сессий.
func (s *Server) Sessions() int64 { return s.sessions.Load() }

// Serve принимает соединения до тех пор, пока сервер не будет закрыт.
func (s *Server) Serve() error {
	if s.ln == nil {
		return errors.New("proxy: сервер не запущен")
	}
	defer s.wg.Wait()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if s.closing.Load() {
				return nil
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return fmt.Errorf("proxy: приём соединения: %w", err)
		}
		if s.cfg.Upstream == nil {
			conn.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(conn)
		}()
	}
}

func (s *Server) handle(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	s.sessions.Add(1)
	defer func() {
		s.sessions.Add(-1)
		conn.Close()
	}()

	cfg := session.Config{
		Secret:           s.cfg.Secret,
		Upstream:         s.cfg.Upstream,
		Logger:           s.log,
		HandshakeTimeout: s.cfg.HandshakeTimeout,
		IdleTimeout:      s.cfg.IdleTimeout,
	}
	err := session.Run(s.ctx, conn, cfg)
	if s.cfg.SessionHook != nil {
		s.cfg.SessionHook(err)
	}
	if err != nil && !s.closing.Load() {
		s.log.Debug("сессия завершена с ошибкой", "remote", conn.RemoteAddr().String(), "err", err)
	}
}

// Close останавливает прокси и дожидается завершения активных сессий.
func (s *Server) Close() error {
	if !s.closing.CompareAndSwap(false, true) {
		return nil
	}
	s.cancel()
	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	return err
}
