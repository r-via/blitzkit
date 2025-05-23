// Package blitzkit provides utility functions and core server components.
package blitzkit

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// GetClientIP extracts the client's IP address from request headers.
// It prioritizes X-Forwarded-For, then uses Fiber's RemoteAddr as a fallback.
func GetClientIP(xForwardedFor, remoteAddr string) string {
	if xForwardedFor != "" {
		ips := strings.Split(xForwardedFor, ",")
		clientIP := strings.TrimSpace(ips[0])
		if clientIP != "" {
			return clientIP
		}
	}
	ipPort := strings.Split(remoteAddr, ":")
	if len(ipPort) > 0 && ipPort[0] != "" {
		return ipPort[0]
	}
	return remoteAddr
}

// defaultDuration returns value if it's positive, otherwise returns defaultValue.
func defaultDuration(value, defaultValue time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return defaultValue
}

// defaultInt returns value if it's non-zero, otherwise returns defaultValue.
func defaultInt(value, defaultValue int) int {
	if value != 0 {
		return value
	}
	return defaultValue
}

// parseDurationEnv parses a duration from an environment variable.
// If the environment variable is set and valid, its value is used.
// Otherwise, if configValue is positive, it's used.
// Otherwise, defaultValue is used. It logs the source of the determined value.
func parseDurationEnv(key string, configValue time.Duration, defaultValue time.Duration, logger *slog.Logger) time.Duration {
	log := logger
	if log == nil {
		log = slog.Default()
	}

	envStr := os.Getenv(key)
	if envStr != "" {
		d, err := time.ParseDuration(envStr)
		if err == nil {
			log.Debug("Using duration from environment variable", "var", key, "value", d)
			return d
		}
		log.Warn("Invalid duration format in environment variable, falling back", "var", key, "value", envStr, "error", err)
	}
	if configValue > 0 {
		log.Debug("Using duration from configuration", "var", key, "value", configValue)
		return configValue
	}
	log.Debug("Using default duration", "var", key, "value", defaultValue)
	return defaultValue
}

// getEnvAsBool retrieves an environment variable and parses it as a boolean.
// "true", "1", "yes", "on" (case-insensitive) are considered true.
// "false", "0", "no", "off" (case-insensitive) are considered false.
// If the environment variable is not set or is invalid, defaultValue is returned.
func getEnvAsBool(logger *slog.Logger, key string, defaultValue bool) bool {
	envStr := os.Getenv(key)
	if envStr != "" {
		valLower := strings.ToLower(envStr)
		if valLower == "true" || valLower == "1" || valLower == "yes" || valLower == "on" {
			logger.Debug("Using boolean from environment variable (true)", "var", key, "value_read", envStr)
			return true
		}
		if valLower == "false" || valLower == "0" || valLower == "no" || valLower == "off" {
			logger.Debug("Using boolean from environment variable (false)", "var", key, "value_read", envStr)
			return false
		}
		logger.Warn("Invalid boolean format in environment variable, using default", "var", key, "value_read", envStr, "default_used", defaultValue)
	}
	logger.Debug("Using default boolean", "var", key, "value_used", defaultValue, "env_value_if_any", envStr)
	return defaultValue
}

// checkDirWritable checks if a given directory path exists and is writable.
// It attempts to create and then delete a temporary file within the directory.
func checkDirWritable(dir string, logger *slog.Logger) error {
	log := logger
	if log == nil {
		log = slog.Default()
	}

	if dir == "" {
		return errors.New("directory path cannot be empty for write check")
	}
	info, err := os.Stat(dir)
	if err != nil {
		log.Error("Cannot check writability, directory stat failed", "path", dir, "error", err)
		return fmt.Errorf("cannot check writability for %s: %w", dir, err)
	}
	if !info.IsDir() {
		log.Error("Cannot check writability, path is not a directory", "path", dir)
		return fmt.Errorf("cannot check writability, %s is not a directory", dir)
	}

	testFileName := ".write_test." + strconv.FormatInt(time.Now().UnixNano(), 10) + ".tmp"
	testFile := filepath.Join(dir, testFileName)

	f, err := os.Create(testFile)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			log.Error("Permission denied: Cannot write in directory", "path", dir)
			return fmt.Errorf("permission denied writing to directory %s: %w", dir, err)
		}
		log.Error("Failed to create test file for write check", "path", testFile, "error", err)
		return fmt.Errorf("cannot create test file in %s: %w", dir, err)
	}

	closeErr := f.Close()
	removeErr := os.Remove(testFile)
	if closeErr != nil {
		log.Warn("Failed to close test file handle after write check", "path", testFile, "error", closeErr)
	}
	if removeErr != nil {
		log.Warn("Failed to remove write test file after creation", "path", testFile, "error", removeErr)
	}

	return nil
}

// ensureDirExists checks if a directory exists at the specified path.
// If it doesn't exist, it attempts to create it, including any necessary parent directories.
// It returns an error if the path exists but is not a directory, or if creation fails.
func ensureDirExists(dir string, logger *slog.Logger) error {
	log := logger
	if log == nil {
		log = slog.Default()
	}
	if dir == "" {
		return errors.New("directory path cannot be empty for ensureDirExists")
	}

	info, err := os.Stat(dir)
	if err == nil {
		if info.IsDir() {
			return nil
		}
		return fmt.Errorf("path %s exists but is not a directory", dir)
	}

	if errors.Is(err, os.ErrNotExist) {
		if errMkdir := os.MkdirAll(dir, 0755); errMkdir != nil {
			log.Error("Failed to create directory path", "path", dir, "error", errMkdir)
			return fmt.Errorf("failed to create directory %s: %w", dir, errMkdir)
		}
		log.Debug("Created directory", "path", dir)
		return nil
	}

	log.Error("Failed to stat directory for ensureDirExists check", "path", dir, "error", err)
	return fmt.Errorf("failed to check directory %s: %w", dir, err)
}

// WantsJSON checks if the request's Accept header indicates a preference for JSON.
func WantsJSON(c *fiber.Ctx) bool {
	if c == nil {
		slog.Default().Warn("WantsJSON called with nil context")
		return false
	}
	return strings.Contains(strings.ToLower(c.Get(fiber.HeaderAccept)), fiber.MIMEApplicationJSON)
}

// getEnvOrDefault retrieves an environment variable, using a configuration value
// or a default value as fallbacks. It logs the source of the determined value.
func getEnvOrDefault(logger *slog.Logger, key, configValue, defaultValue string) string {
	log := logger
	if log == nil {
		log = slog.Default()
	}

	if envVal := os.Getenv(key); envVal != "" {
		log.Debug("Using value from environment variable", "var", key)
		return envVal
	}
	if configValue != "" {
		log.Debug("Using value from configuration", "var", key)
		return configValue
	}
	log.Debug("Using default value", "var", key, "value", defaultValue)
	return defaultValue
}
