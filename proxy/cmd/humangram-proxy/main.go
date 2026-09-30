// Команда humangram-proxy запускает локальный прокси MTProto из командной
// строки. Используется при разработке и диагностике: приложения HumanGram
// Desktop и HumanGram Android встраивают тот же модуль в виде библиотеки
// и не требуют отдельного процесса.
//
// Пример запуска:
//
//	humangram-proxy --secret 00112233445566778899aabbccddeeff
//	humangram-proxy --upstream websocket --ws-url wss://example.org/mtproto
//
// Данный файл распространяется по лицензии MIT.
// Copyright (c) 2026 HumanGram contributors
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/HumanGram/humangram-proxy"
)

func main() {
	cfg := humangram.Config{}
	flag.StringVar(&cfg.Secret, "secret", os.Getenv("HUMANGRAM_SECRET"), "секрет прокси: 32 шестнадцатеричных символа")
	flag.StringVar(&cfg.Host, "host", "127.0.0.1", "адрес прослушивания")
	flag.IntVar(&cfg.Port, "port", 0, "порт прослушивания; 0 — свободный порт")
	flag.StringVar((*string)(&cfg.Upstream), "upstream", "direct", "восходящий транспорт: direct или websocket")
	flag.StringVar(&cfg.WebSocketURL, "ws-url", os.Getenv("HUMANGRAM_WS_URL"), "адрес WebSocket-эндпоинта")
	flag.BoolVar(&cfg.InsecureSkipVerify, "insecure", false, "не проверять сертификат TLS WebSocket-эндпоинта")
	flag.BoolVar(&cfg.IPv6Only, "ipv6", false, "использовать только адреса IPv6")
	flag.IntVar(&dcOverrideFlag, "dc", 0, "принудительно переопределить адрес дата-центра (см. -dc-addr)")
	flag.StringVar(&dcAddrFlag, "dc-addr", "", "адреса дата-центра из -dc в формате host:port[,host:port]")
	flag.DurationVar(&cfg.IdleTimeout, "idle-timeout", 0, "таймаут простоя сессии; 0 — без ограничения")
	flag.BoolVar(&debugFlag, "debug", false, "подробный журнал")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println("HumanGram-Proxy", humangram.Version)
		return
	}

	level := slog.LevelInfo
	if debugFlag {
		level = slog.LevelDebug
	}
	cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if dcOverrideFlag > 0 {
		cfg.DCOverrides = map[string]string{fmt.Sprint(dcOverrideFlag): dcAddrFlag}
	}
	if strings.EqualFold(string(cfg.Upstream), "websocket") {
		cfg.Upstream = humangram.UpstreamWebSocket
	}
	if cfg.Upstream == humangram.UpstreamWebSocket && cfg.WebSocketURL == "" {
		fatal("для транспорта websocket требуется --ws-url")
	}
	if cfg.Secret == "" {
		cfg.Logger.Warn("секрет не задан: клиент должен использовать режим abridged без шифрования")
	}

	p, err := humangram.Start(context.Background(), cfg)
	if err != nil {
		fatal(err.Error())
	}
	fmt.Printf("HumanGram-Proxy %s слушает %s\n", humangram.Version, p.Addr())
	fmt.Println("Настройте прокси в клиенте Telegram: тип MTProxy, адрес и порт выше.")
	if cfg.Secret != "" {
		fmt.Printf("Секрет: %s\n", cfg.Secret)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-sig:
			fmt.Println("\nОстановка…")
			_ = p.Stop()
			return
		case <-ticker.C:
			cfg.Logger.Debug("статус прокси", "сессий", p.Sessions())
		}
	}
}

var (
	dcOverrideFlag int
	dcAddrFlag     string
	debugFlag      bool
)

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "HumanGram-Proxy:", msg)
	os.Exit(1)
}
