package auth

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type ClientIPResolver struct {
	mode    string
	trusted []netip.Prefix
}

func NewClientIPResolver(mode string, trusted []netip.Prefix) *ClientIPResolver {
	return &ClientIPResolver{mode: mode, trusted: append([]netip.Prefix(nil), trusted...)}
}

func DirectPeerIP(r *http.Request) (netip.Addr, bool) {
	remote := strings.TrimSpace(r.RemoteAddr)
	host, _, err := net.SplitHostPort(remote)
	if err == nil {
		remote = host
	}
	address, err := netip.ParseAddr(remote)
	if err != nil {
		return netip.Addr{}, false
	}
	return address.WithZone("").Unmap(), true
}

func (r *ClientIPResolver) ClientIP(req *http.Request) (netip.Addr, bool) {
	peer, ok := DirectPeerIP(req)
	if !ok || r == nil || r.mode == "remote" || !r.isTrusted(peer) {
		return peer, ok
	}

	switch r.mode {
	case "x-forwarded-for":
		rawValues := req.Header.Values("X-Forwarded-For")
		values := strings.Split(strings.Join(rawValues, ","), ",")
		if len(values) == 1 && strings.TrimSpace(values[0]) == "" {
			return peer, true
		}
		chain := make([]netip.Addr, 0, len(values))
		for _, value := range values {
			address, err := netip.ParseAddr(strings.TrimSpace(value))
			if err != nil || address.Zone() != "" {
				return peer, true
			}
			chain = append(chain, address.Unmap())
		}
		for i := len(chain) - 1; i >= 0; i-- {
			if !r.isTrusted(chain[i]) {
				return chain[i], true
			}
		}
		return peer, true
	case "cf-connecting-ip":
		rawValues := req.Header.Values("CF-Connecting-IP")
		if len(rawValues) != 1 {
			return peer, true
		}
		value := strings.TrimSpace(rawValues[0])
		if value == "" || strings.Contains(value, ",") {
			return peer, true
		}
		address, err := netip.ParseAddr(value)
		if err != nil || address.Zone() != "" {
			return peer, true
		}
		return address.Unmap(), true
	default:
		return peer, true
	}
}

func (r *ClientIPResolver) isTrusted(address netip.Addr) bool {
	for _, prefix := range r.trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
