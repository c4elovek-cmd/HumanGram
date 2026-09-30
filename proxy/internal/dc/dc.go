// Package dc хранит справочник адресов дата-центров Telegram.
//
// Значения совпадают со встроенным списком kBuiltInDcs в tdesktop
// (Telegram/SourceFiles/mtproto/mtproto_dc_options.cpp).
//
// Данный файл распространяется по лицензии MIT.
// Copyright (c) 2026 HumanGram contributors
package dc

import (
	"fmt"
	"strconv"
	"strings"
)

// Endpoint — адрес дата-центра.
type Endpoint struct {
	IP   string
	Port int
}

// String возвращает адрес в формате host:port.
func (e Endpoint) String() string { return e.IP + ":" + strconv.Itoa(e.Port) }

// builtIn содержит адреса основного кластера дата-центров.
var builtIn = map[int][]Endpoint{
	1: {{IP: "149.154.175.50", Port: 443}},
	2: {
		{IP: "149.154.167.51", Port: 443},
		{IP: "95.161.76.100", Port: 443},
	},
	3: {{IP: "149.154.175.100", Port: 443}},
	4: {{IP: "149.154.167.91", Port: 443}},
	5: {{IP: "149.154.171.5", Port: 443}},
}

// builtInIPv6 содержит адреса основного кластера в формате IPv6.
var builtInIPv6 = map[int][]Endpoint{
	1: {{IP: "2001:0b28:f23d:f001::a", Port: 443}},
	2: {{IP: "2001:67c:4e8:f002::a", Port: 443}},
	3: {{IP: "2001:0b28:f23d:f003::a", Port: 443}},
	4: {{IP: "2001:67c:4e8:f004::a", Port: 443}},
	5: {{IP: "2001:0b28:f23f:f005::a", Port: 443}},
}

// DefaultDC — дата-центр, используемый, если клиент не сообщил индекс.
const DefaultDC = 2

// Resolver разрешает индекс дата-центра в список адресов.
//
// Пустая карта означает использование встроенного справочника.
type Resolver struct {
	overrides map[int][]Endpoint
	ipv6Only  bool
}

// NewResolver создаёт разрешитель на основе встроенного справочника.
func NewResolver() *Resolver { return &Resolver{overrides: map[int][]Endpoint{}} }

// WithOverride задаёт адреса для конкретного дата-центра вместо встроенных.
// Строка имеет формат "host:port" либо "ip1:port1,ip2:port2".
func (r *Resolver) WithOverride(dcID int, spec string) error {
	endpoints, err := ParseEndpoints(spec)
	if err != nil {
		return fmt.Errorf("dc %d: %w", dcID, err)
	}
	r.overrides[dcID] = endpoints
	return nil
}

// WithIPv6Only ограничивает разрешение адресами семейства IPv6.
func (r *Resolver) WithIPv6Only(v bool) *Resolver {
	r.ipv6Only = v
	return r
}

// ParseEndpoints разбирает список адресов вида "host:port,host:port".
func ParseEndpoints(spec string) ([]Endpoint, error) {
	var out []Endpoint
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ep, err := ParseEndpoint(part)
		if err != nil {
			return nil, err
		}
		out = append(out, ep)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("пустой список адресов")
	}
	return out, nil
}

// ParseEndpoint разбирает адрес вида "host:port". Для IPv6-адресов,
// заключённых в квадратные скобки, порт указывается после закрывающей скобки.
func ParseEndpoint(spec string) (Endpoint, error) {
	if strings.HasPrefix(spec, "[") {
		end := strings.LastIndex(spec, "]")
		if end < 0 {
			return Endpoint{}, fmt.Errorf("незакрытая скобка в адресе %q", spec)
		}
		host := spec[1:end]
		portPart := spec[end+1:]
		if !strings.HasPrefix(portPart, ":") {
			return Endpoint{}, fmt.Errorf("отсутствует порт в адресе %q", spec)
		}
		port, err := strconv.Atoi(portPart[1:])
		if err != nil {
			return Endpoint{}, fmt.Errorf("неверный порт в адресе %q", spec)
		}
		return Endpoint{IP: host, Port: port}, nil
	}
	idx := strings.LastIndex(spec, ":")
	if idx < 0 {
		return Endpoint{}, fmt.Errorf("отсутствует порт в адресе %q", spec)
	}
	host := spec[:idx]
	port, err := strconv.Atoi(spec[idx+1:])
	if err != nil {
		return Endpoint{}, fmt.Errorf("неверный порт в адресе %q", spec)
	}
	return Endpoint{IP: host, Port: port}, nil
}

// Resolve возвращает адреса для индекса дата-центра dcID.
// Медиакластеры используют те же адреса, что и основной кластер:
// различие кодируется знаком индекса на уровне протокола.
func (r *Resolver) Resolve(dcID int) ([]Endpoint, error) {
	if dcID < 0 {
		dcID = -dcID
	}
	if dcID == 0 {
		dcID = DefaultDC
	}
	if eps, ok := r.overrides[dcID]; ok {
		return eps, nil
	}
	if r.ipv6Only {
		if eps, ok := builtInIPv6[dcID]; ok {
			return eps, nil
		}
		return nil, fmt.Errorf("неизвестный дата-центр %d", dcID)
	}
	if eps, ok := builtIn[dcID]; ok {
		return eps, nil
	}
	if eps, ok := builtInIPv6[dcID]; ok {
		return eps, nil
	}
	return nil, fmt.Errorf("неизвестный дата-центр %d", dcID)
}

// Known возвращает список всех известных идентификаторов дата-центров.
func (r *Resolver) Known() []int {
	seen := map[int]bool{}
	for id := range builtIn {
		seen[id] = true
	}
	for id := range r.overrides {
		seen[id] = true
	}
	out := make([]int, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
