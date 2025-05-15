// File: middleware.go
// Description: Contient la configuration et l'enregistrement des middlewares de base
//
//	pour l'application Fiber, tels que Recover, CORS, les en-têtes de sécurité,
//	et la journalisation des requêtes.
package blitzkit

import (
	"context"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

// setupBaseMiddlewares configure et enregistre les middlewares fondamentaux pour l'instance Fiber.
// Ceci inclut Recover, CORS (configuré via des variables d'environnement),
// l'ajout d'en-têtes de sécurité (définis dans la configuration), la journalisation des requêtes,
// et tout middleware personnalisé fourni dans la configuration.
func (s *Server) setupBaseMiddlewares() {
	s.logger.Debug("Setting up base middlewares...")

	s.app.Use(recover.New(recover.Config{
		EnableStackTrace: s.config.DevMode,
	}))
	s.logger.Debug("Added Recover middleware.")

	corsConfig := cors.Config{
		AllowMethods:     "GET,POST,PUT,DELETE,PATCH,HEAD,OPTIONS",
		AllowHeaders:     "Origin,Content-Type,Accept,Authorization,X-CSRF-Token,HX-Request,HX-Trigger,HX-Target,HX-Current-URL,Content-Length,X-Api-Key",
		ExposeHeaders:    "Content-Length,HX-Location,HX-Redirect,HX-Refresh,HX-Retarget,HX-Reswap,HX-Trigger,HX-Trigger-After-Settle,HX-Trigger-After-Swap",
		AllowCredentials: true,
		MaxAge:           86400,
	}
	allowedOrigins := getEnvOrDefault(s.logger, "CORS_ALLOW_ORIGINS", "", "")
	if s.config.DevMode {
		if allowedOrigins == "" {
			allowedOrigins = "*"
			if corsConfig.AllowCredentials {
				s.logger.Error("FATAL: Insecure CORS dev setup: AllowOrigins='*' with AllowCredentials=true. Set CORS_ALLOW_ORIGINS or disable credentials.")
				panic("Insecure CORS dev setup")
			}
		}
	} else {
		if allowedOrigins == "" || (allowedOrigins == "*" && corsConfig.AllowCredentials) {
			s.logger.Error("FATAL: Invalid CORS production config (CORS_ALLOW_ORIGINS missing or '*' with credentials)")
			panic("Invalid CORS production config")
		}
	}
	corsConfig.AllowOrigins = allowedOrigins
	s.app.Use(cors.New(corsConfig))
	s.logger.Info("Added CORS middleware.", "applied_origins", allowedOrigins, "allow_credentials", corsConfig.AllowCredentials)

	if len(s.config.SecurityHeaders) > 0 {
		s.app.Use(func(c *fiber.Ctx) error {
			for key, value := range s.config.SecurityHeaders {
				c.Set(key, value)
			}
			return c.Next()
		})
		s.logger.Info("Added Security Headers middleware.", "count", len(s.config.SecurityHeaders))
	}

	s.app.Use(s.logRequests)
	s.logger.Debug("Added Request Logging middleware.")

	for i, mwFunc := range s.config.CustomMiddlewares {
		s.app.Use(mwFunc)
		s.logger.Debug("Added custom middleware from config", "index", i+1)
	}
}

// logRequests est un middleware Fiber qui enregistre les détails de chaque requête traitée.
// Il capture la méthode, le chemin, le statut de la réponse, la durée de traitement,
// l'adresse IP du client, la taille de la réponse, l'agent utilisateur, l'ID de la requête (si disponible),
// et le statut du cache (si défini par `RenderPage`/`RenderBytesPage`). Le niveau de log (Debug, Info, Warn, Error)
// est ajusté en fonction du statut de la réponse et de la présence d'une erreur.
//
// Args:
//
//	c (*fiber.Ctx): Le contexte Fiber de la requête.
//
// Returns:
//
//	error: L'erreur retournée par le prochain middleware ou handler dans la chaîne.
func (s *Server) logRequests(c *fiber.Ctx) error {
	start := time.Now()
	clientIP := GetClientIP(c.Get(fiber.HeaderXForwardedFor), c.IP())

	err := c.Next()

	duration := time.Since(start)
	status := c.Response().StatusCode()

	logLevel := slog.LevelInfo
	if s.config.DevMode && status < 400 && err == nil {
		logLevel = slog.LevelDebug
	}
	if status >= 500 {
		logLevel = slog.LevelError
	} else if status >= 400 {
		logLevel = slog.LevelWarn
	}
	if err != nil && logLevel < slog.LevelError {
		logLevel = slog.LevelError
	}

	attrs := []slog.Attr{
		slog.String("method", c.Method()),
		slog.String("path", c.OriginalURL()),
		slog.Int("status", status),
		slog.Duration("duration_ms", duration.Round(time.Millisecond)),
		slog.String("ip", clientIP),
		slog.Int("resp_size_bytes", len(c.Response().Body())),
	}
	if userAgent := c.Get(fiber.HeaderUserAgent); userAgent != "" {
		attrs = append(attrs, slog.String("user_agent", userAgent))
	}
	reqIDVal := c.Locals("requestid")
	if reqIDStr, ok := reqIDVal.(string); ok && reqIDStr != "" {
		attrs = append(attrs, slog.String("request_id", reqIDStr))
	} else if reqIDHeader := c.Get(fiber.HeaderXRequestID); reqIDHeader != "" {
		attrs = append(attrs, slog.String("request_id", reqIDHeader))
	}

	cacheStatusBytes := c.Response().Header.Peek(HeaderCacheStatus)
	if len(cacheStatusBytes) > 0 {
		attrs = append(attrs, slog.String("cache", string(cacheStatusBytes)))
	}

	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}

	s.logger.LogAttrs(context.Background(), logLevel, "Request handled", attrs...)
	return err
}
