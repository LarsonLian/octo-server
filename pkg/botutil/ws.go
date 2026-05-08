package botutil

import (
	"fmt"
	"net"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/config"
)

// DeriveWSURL derives the WuKongIM WebSocket URL from server config.
func DeriveWSURL(cfg *config.Config) string {
	baseURL := strings.TrimSpace(cfg.External.BaseURL)
	if baseURL != "" {
		host := baseURL
		host = strings.TrimPrefix(host, "https://")
		host = strings.TrimPrefix(host, "http://")
		if idx := strings.Index(host, "/"); idx >= 0 {
			host = host[:idx]
		}
		// Try net.SplitHostPort to handle both IPv4:port and [IPv6]:port
		if h, _, err := net.SplitHostPort(host); err == nil {
			// Has explicit port → direct mode, use WuKongIM 5200
			return fmt.Sprintf("ws://%s:5200", h)
		}
		// Domain mode → Nginx reverse proxy
		if strings.HasPrefix(baseURL, "https://") {
			return fmt.Sprintf("wss://%s/ws", host)
		}
		return fmt.Sprintf("ws://%s/ws", host)
	}
	// Fallback: derive from WuKongIM API URL
	apiURL := cfg.WuKongIM.APIURL
	host := apiURL
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	// External.IP overrides derived host (direct-access deployments)
	if strings.TrimSpace(cfg.External.IP) != "" {
		host = cfg.External.IP
	}
	return fmt.Sprintf("ws://%s:5200", host)
}
