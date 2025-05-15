// File: pkg/webserver/utils.go
// Description: Fournit diverses fonctions utilitaires pour le package webserver,
//
//	telles que l'extraction d'IP client, la gestion des valeurs par défaut,
//	la vérification de répertoires, et la détection des requêtes JSON.
package webserver

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

// GetClientIP extrait l'adresse IP du client à partir des en-têtes de la requête.
// Priorise l'en-tête `X-Forwarded-For` (en prenant la première IP de la liste si présente),
// puis utilise l'adresse distante (`RemoteAddr`) du contexte Fiber comme repli.
//
// Args:
//
//	xForwardedFor (string): La valeur de l'en-tête `X-Forwarded-For`.
//	remoteAddr (string): L'adresse distante provenant du contexte Fiber.
//
// Returns:
//
//	string: L'adresse IP extraite du client.
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

// defaultDuration retourne la `value` si elle est positive (> 0), sinon retourne `defaultValue`.
// Utile pour définir des timeouts ou intervalles avec des valeurs par défaut saines.
//
// Args:
//
//	value (time.Duration): La valeur de durée potentielle.
//	defaultValue (time.Duration): La valeur par défaut à utiliser si `value` n'est pas positive.
//
// Returns:
//
//	time.Duration: La durée effective.
func defaultDuration(value, defaultValue time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return defaultValue
}

// defaultInt retourne la `value` si elle est différente de zéro, sinon retourne `defaultValue`.
//
// Args:
//
//	value (int): La valeur entière potentielle.
//	defaultValue (int): La valeur par défaut à utiliser si `value` est zéro.
//
// Returns:
//
//	int: La valeur entière effective.
func defaultInt(value, defaultValue int) int {
	if value != 0 {
		return value
	}
	return defaultValue
}

// parseDurationEnv tente de parser une durée à partir d'une variable d'environnement.
// Si la variable d'environnement existe et est valide, sa valeur est utilisée.
// Sinon, si `configValue` est positive, elle est utilisée.
// Sinon, `defaultValue` est utilisée. Logue la source de la valeur utilisée.
//
// Args:
//
//	key (string): Le nom de la variable d'environnement.
//	configValue (time.Duration): La valeur provenant potentiellement d'une structure de configuration.
//	defaultValue (time.Duration): La valeur par défaut ultime.
//	logger (*slog.Logger): Le logger pour enregistrer la source de la valeur.
//
// Returns:
//
//	time.Duration: La durée effective déterminée.
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

// checkDirWritable vérifie si un répertoire donné existe et est accessible en écriture
// en tentant de créer puis supprimer un fichier temporaire à l'intérieur.
//
// Args:
//
//	dir (string): Le chemin du répertoire à vérifier.
//	logger (*slog.Logger): Le logger pour enregistrer les erreurs ou avertissements.
//
// Returns:
//
//	error: Une erreur si le chemin est invalide, n'est pas un répertoire,
//	       n'est pas accessible en écriture, ou si une erreur système survient. Nil si le répertoire est accessible en écriture.
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

// ensureDirExists vérifie si un répertoire existe au chemin spécifié. S'il n'existe pas,
// il tente de le créer (ainsi que tous les répertoires parents nécessaires).
// Retourne une erreur si le chemin existe mais n'est pas un répertoire, ou si la création échoue.
//
// Args:
//
//	dir (string): Le chemin du répertoire à vérifier/créer.
//	logger (*slog.Logger): Le logger pour enregistrer les actions ou erreurs.
//
// Returns:
//
//	error: Une erreur si le chemin est invalide, si ce n'est pas un répertoire, ou si la création échoue. Nil sinon.
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

// WantsJSON vérifie si l'en-tête `Accept` de la requête indique une préférence pour le format JSON.
//
// Args:
//
//	c (*fiber.Ctx): Le contexte Fiber de la requête.
//
// Returns:
//
//	bool: true si l'en-tête `Accept` contient "application/json", sinon false.
func WantsJSON(c *fiber.Ctx) bool {
	if c == nil {
		slog.Default().Warn("WantsJSON called with nil context")
		return false
	}
	return strings.Contains(strings.ToLower(c.Get(fiber.HeaderAccept)), fiber.MIMEApplicationJSON)
}

// getEnvOrDefault récupère une variable d'environnement, en utilisant une valeur de configuration
// ou une valeur par défaut comme repli. Logue la source de la valeur utilisée.
//
// Args:
//
//	logger (*slog.Logger): Le logger pour enregistrer la source.
//	key (string): Le nom de la variable d'environnement.
//	configValue (string): La valeur provenant potentiellement d'une configuration.
//	defaultValue (string): La valeur par défaut ultime.
//
// Returns:
//
//	string: La valeur effective déterminée.
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
