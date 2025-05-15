// File: pkg/blitzkit/error_handling.go
// Description: Définit le gestionnaire d'erreurs centralisé pour l'application Fiber.
//
//	Il intercepte les erreurs, détermine le code de statut HTTP approprié,
//	logue l'erreur, et renvoie une réponse formatée (HTML via composant Templ, JSON, ou texte brut).
package blitzkit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/a-h/templ"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
)

// handleError est le gestionnaire d'erreurs global enregistré auprès de Fiber (via fiber.Config).
// Il est appelé automatiquement par Fiber lorsqu'un handler retourne une erreur ou qu'un panic se produit (si Recover middleware est utilisé).
// Il détermine le code d'erreur et le message, logue l'incident, puis tente de renvoyer une réponse appropriée :
// 1. Composant d'erreur Templ si configuré (`ErrorComponentGenerator`) et si le client accepte HTML.
// 2. Réponse JSON si le client accepte JSON (`Accept: application/json`).
// 3. Message texte brut en dernier recours.
// En mode production, les détails des erreurs 5xx internes sont masqués au client.
//
// Args:
//
//	c (*fiber.Ctx): Le contexte Fiber de la requête ayant échoué.
//	err (error): L'erreur qui s'est produite.
//
// Returns:
//
//	error: Une erreur nil si la réponse d'erreur a été envoyée avec succès, ou une erreur si l'envoi final échoue.
func (s *Server) handleError(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := "Internal Server Error"

	var fe *fiber.Error
	if errors.As(err, &fe) {
		code = fe.Code
		message = fe.Message
	} else {
		s.logger.Error("Internal server error", "path", c.Path(), "method", c.Method(),
			"ip", GetClientIP(c.Get(fiber.HeaderXForwardedFor), c.IP()), "error", err)
		if s.config.DevMode {
			message = fmt.Sprintf("%s: %v", message, err)
		}
	}

	logLevel := slog.LevelWarn
	if code >= 500 {
		logLevel = slog.LevelError
	}
	s.logger.Log(context.Background(), logLevel, "Request error handled",
		"code", code, "message", message, "path", c.Path(), "method", c.Method(),
		"ip", GetClientIP(c.Get(fiber.HeaderXForwardedFor), c.IP()), "original_error_type", fmt.Sprintf("%T", err))

	c.Status(code)

	clientWantsJSON := WantsJSON(c)

	if s.config.ErrorComponentGenerator != nil && !clientWantsJSON {
		errorComponent := s.config.ErrorComponentGenerator(err, code, s.config.DevMode)
		if errorComponent != nil {
			renderErr := adaptor.HTTPHandler(templ.Handler(errorComponent))(c)
			if renderErr == nil {
				return nil
			}
			s.logger.Error("Failed to render error component, falling back", "code", code, "render_error", renderErr)
		}
	}

	if clientWantsJSON {
		errorMsgJson := message
		if code >= 500 && !s.config.DevMode {
			errorMsgJson = "Internal Server Error"
		}
		return c.JSON(fiber.Map{"error": errorMsgJson})
	}

	finalMessage := fmt.Sprintf("%d: %s", code, message)

	return c.SendString(finalMessage)
}
