package subproxy

import (
	"bufio"
	"os"
	"strings"
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

func LoadExtraConfig(filePath string) (*ExtraConfig, error) {
	if filePath == "" {
		return nil, nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

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
