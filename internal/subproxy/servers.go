package subproxy

import (
	"bufio"
	"os"
	"strings"

	"rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

var validSchemes = []string{
	"vless://",
	"vmess://",
	"trojan://",
	"ss://",
	"ssr://",
	"hysteria://",
	"hysteria2://",
	"hy2://",
	"tuic://",
	"wg://",
	"wireguard://",
}

type ExtraConfig struct {
	Headers map[string]string
	Servers []string
}

// LoadExtraConfig loads and parses an extra configuration file at filePath.
// It returns (nil, nil) when filePath is empty.
// The file may contain a headers section followed by server entries. Header lines
// are in the form "Key: Value" (both parts trimmed) and are collected into
// ExtraConfig.Headers. The header section ends when a blank line is encountered
// or when a server entry is seen. Lines that start with '#' are treated as
// comments and ignored. Server entries are lines whose scheme matches the
// package's recognised prefixes (for example: "vless://", "vmess://",
// "trojan://", "ss://", "ssr://", "hysteria://", "hysteria2://", "hy2://",
// "tuic://", "wg://", "wireguard://") and are collected into ExtraConfig.Servers.
// Returns a non-nil error if the file cannot be opened or if a read/scan error occurs.
func LoadExtraConfig(filePath string) (*ExtraConfig, error) {
	if filePath == "" {
		return nil, nil
	}

	// #nosec G304 // filePath is validated by caller - only alphanumeric with underscore
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			logger.Debug("Failed to close extra config file", zap.Error(closeErr))
		}
	}()

	cfg := &ExtraConfig{
		Headers: make(map[string]string),
	}

	scanner := bufio.NewScanner(f)
	inHeaders := true

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			if inHeaders {
				inHeaders = false
			}
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		if inHeaders {
			if isValidServer(line) {
				inHeaders = false
				cfg.Servers = append(cfg.Servers, line)
				continue
			}
			if idx := strings.Index(line, ":"); idx > 0 {
				key := strings.TrimSpace(line[:idx])
				value := strings.TrimSpace(line[idx+1:])
				if key != "" && value != "" {
					cfg.Headers[key] = value
				}
			}
			continue
		}

		if isValidServer(line) {
			cfg.Servers = append(cfg.Servers, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func isValidServer(line string) bool {
	lower := strings.ToLower(line)
	for _, scheme := range validSchemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}
