package subproxy

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
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

// validateExtraServersPath ensures the file path is safe and within allowed bounds.
// It prevents directory traversal attacks by checking for ".." and ensuring the
// resolved absolute path is within the project's data directory or other safe locations.
func validateExtraServersPath(filePath string) error {
	if filePath == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	// Check for directory traversal attempts in the original path
	// This must be done BEFORE cleaning because Clean() resolves ".."
	if strings.Contains(filePath, "..") {
		return fmt.Errorf("invalid file path: directory traversal detected")
	}

	// Get absolute path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Clean the path to resolve any . elements
	cleaned := filepath.Clean(absPath)

	// Prevent access to system directories (case-insensitive for safety)
	lowerPath := strings.ToLower(cleaned)
	dangerousPrefixes := []string{
		"/etc/", "/root/", "/sys/", "/proc/", "/dev/", "/var/run/",
		"C:\\Windows\\", "C:\\Program Files\\", "C:\\Program Files (x86)\\", // Windows
	}
	for _, prefix := range dangerousPrefixes {
		if strings.HasPrefix(lowerPath, prefix) {
			return fmt.Errorf("access to system directories is forbidden")
		}
	}

	// Ensure path is not root
	if cleaned == "/" || cleaned == "C:\\" || cleaned == "D:\\" {
		return fmt.Errorf("cannot use root directory")
	}

	return nil
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

	// Validate file path to prevent directory traversal attacks
	if err := validateExtraServersPath(filePath); err != nil {
		return nil, fmt.Errorf("invalid extra servers file path: %w", err)
	}

	// #nosec G304 // filePath validated above (no directory traversal, no system paths)
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
