# Package `webserver` - Fondation pour Serveur Web Go Haute Performance

**Version:**
**Go Version Requise:** 1.21+ (principalement pour `slog`)

## 1. Overview

Le package `webserver` fournit une base solide et performante pour la création d'applications web en Go. Il s'appuie sur le framework **Fiber v2** et intègre des fonctionnalités essentielles telles qu'un système de cache à deux niveaux (mémoire et disque via BadgerDB), le traitement des assets statiques, la journalisation structurée avec `slog`, et le monitoring via Prometheus.

Conçu pour être réutilisable, il prend en charge la configuration de base du serveur, la gestion des erreurs, et offre des mécanismes pour le rendu de pages (notamment avec des templates comme Templ, bien que non directement imposé) et de données brutes avec mise en cache.

**Fonctionnalités Clés (Basées sur le Code Actuel):**

*   **Framework Fiber v2**: Utilisation directe de Fiber pour la performance et une API familière.
*   **Cache Hybride L1/L2**:
    *   **L1 (Mémoire)**: `patrickmn/go-cache` pour un accès ultra-rapide.
    *   **L2 (Disque)**: `dgraph-io/badger/v4` pour la persistance, avec gestion du cycle de vie (GC).
    *   Méthodes `RenderPage` et `RenderBytesPage` pour servir du contenu via le cache.
*   **Invalidation Manuelle du Cache**: Méthodes `Invalidate(key)` et `Flush()` pour supprimer des clés spécifiques ou vider l'intégralité du cache L1/L2 programmatiquement.
*   **Préchauffage du Cache (Warmup)**:
    *   `RegisterForPageWarmup` et `RegisterForBytesWarmup` pour enregistrer des générateurs.
    *   `ExecuteWarmup` pour pré-remplir le cache au démarrage.
*   **Traitement des Assets Statiques (`StaticProcessor`)**:
    *   Minification CSS/JS (depuis `SourcesDir`) et copie de fichiers (depuis `StaticsDir`) vers `PublicDir` au démarrage.
    *   Utilise `tdewolff/minify/v2`.
    *   Priorise `.debug.js` en mode développement.
*   **Observabilité (Monitoring)**:
    *   Métriques **Prometheus** via `/metrics` (activable via `Config.EnableMetrics`).
    *   Endpoint de Health Check `GET /health` (vérifie L2 si configuré).
*   **Logging Structuré avec `slog`**: Logger `slog` configurable, avec middleware de journalisation des requêtes.
*   **Gestion des Erreurs Centralisée**: Un `ErrorHandler` par défaut pour Fiber qui logue les erreurs et retourne des réponses appropriées (HTML via `ErrorComponentGenerator` ou JSON).
*   **Configuration Flexible**: Via la struct `Config` et certaines variables d'environnement.
*   **Validation au Démarrage**: Vérification des chemins et permissions pour les répertoires critiques.
*   **Utilitaires**: Fonctions pour la génération de sitemap XML (`GenerateSitemapXMLBytes`), l'extraction d'IP, la gestion des valeurs par défaut, etc.

---

## 2. Introduction

### 2.1 Objectif du Document
Ce README sert de guide de référence pour les développeurs utilisant le package `webserver`. Il détaille l'architecture, la configuration, l'API publique, et les mécanismes internes.

### 2.2 Contexte d'Utilisation
Idéal pour les applications web Go nécessitant :
*   Haute performance avec Fiber.
*   Rendu côté serveur avec un système de cache robuste.
*   Gestion des assets statiques.
*   Logging structuré et monitoring.

### 2.3 Philosophie
*   **Fondation Solide**: Fournir les briques essentielles pour un serveur web moderne.
*   **Configuration Explicite**: Une struct `Config` claire, avec des surcharges via variables d'environnement.
*   **Proche de Fiber**: Encourager l'utilisation directe de Fiber pour le routage et la gestion des requêtes, tout en fournissant des composants d'infrastructure (cache, statics).

### 2.4 Technologies et Dépendances Principales
*   **Go** (1.21+ recommandé).
*   **Fiber v2** (`github.com/gofiber/fiber/v2`).
*   **Templ** (`github.com/a-h/templ`): Supporté pour le rendu, mais l'intégration se fait via l'interface `templ.Component` passée aux générateurs.
*   **go-cache** (`github.com/patrickmn/go-cache`): Cache L1.
*   **BadgerDB v4** (`github.com/dgraph-io/badger/v4`): Cache L2.
*   **minify v2** (`github.com/tdewolff/minify/v2`): Minification CSS/JS.
*   **slog** (`log/slog`): Logging.
*   **Prometheus Client** (`github.com/prometheus/client_golang`).
*   **Fiber Prometheus Middleware** (`github.com/ansrivas/fiberprometheus/v2`).

---

## 3. Concepts Fondamentaux

### 3.1 Handlers Fiber Standards
Le package `webserver` s'attend à ce que vous utilisiez des handlers Fiber standards, typiquement de la forme `func(c *fiber.Ctx) error`.

### 3.2 Fonctions Génératrices (Generator Functions)
Pour le rendu avec cache (`RenderPage`, `RenderBytesPage`) et le préchauffage (`ExecuteWarmup`), le package utilise des fonctions génératrices :
*   **`PageGeneratorFunc`**: `func() (page templ.Component, lastModified int64, err error)`
    *   Retourne un `templ.Component` (ou tout autre objet implémentant `templ.Component`).
    *   `lastModified`: Timestamp Unix de la dernière modification des données sources.
    *   `err`: Erreur de génération.
*   **`BytesGeneratorFunc`**: `func() (data []byte, lastModified int64, err error)`
    *   Similaire, mais retourne des `[]byte` (ex: XML, JSON).

Ces fonctions sont appelées par le système de cache en cas de "miss" ou d'expiration.

### 3.3 Cache L1/L2 et `CacheTTLInfo`
*   **L1 (Mémoire)**: Rapide, pour les accès fréquents. TTL contrôlé par `Config.CacheL1DefaultTTL` ou `cache.NoExpiration` si `CacheTTLInfo.IsInfinite` est `true`.
*   **L2 (Disque - BadgerDB)**: Persistant. TTL contrôlé par `Config.CacheL2DefaultTTL` ou persistant si `CacheTTLInfo.IsInfinite` est `true`.
*   **`CacheTTLInfo{IsInfinite: bool}`**: Utilisé dans `RenderPage`/`RenderBytesPage` et lors de l'enregistrement pour le warmup pour indiquer si l'élément doit avoir une durée de vie "infinie" (pas de TTL basé sur le temps). Si `false`, les TTLs par défaut de la configuration sont appliqués.

### 3.4 Traitement des Assets Statiques
Le `StaticProcessor` est exécuté une fois au démarrage du serveur (`NewServer`):
1.  Il purge (supprime et recrée) le `Config.PublicDir`.
2.  Les fichiers CSS/JS de `Config.SourcesDir` sont minifiés et écrits dans `PublicDir`. Les fichiers `.debug.js` sont priorisés si `Config.DevMode` est `true`.
3.  Les fichiers de `Config.StaticsDir` (images, fonts, etc.) sont copiés tels quels dans `PublicDir`.
L'application est ensuite responsable de servir les fichiers depuis `Config.PublicDir` en utilisant `app.Static("/", cfg.PublicDir)`.

---

## 4. Démarrage Rapide

### 4.1 Prérequis
*   Go 1.21+ installé.
*   Un projet Go initialisé (`go mod init monprojet`).
*   Templ installé si vous l'utilisez (`go install github.com/a-h/templ/cmd/templ@latest`).

### 4.2 Installation du Package
```bash
go get github.com/chemin/vers/votre/webserver # Assurez-vous que le chemin est correct
go mod tidy
```

### 4.3 Exemple Minimal `main.go`

```go
package main

import (
	"fmt"
	"log/slog"
	"monprojet/views" // Supposons un package 'views' généré par Templ
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/a-h/templ"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/csrf"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	// Remplacez par le chemin réel de votre package webserver
	"monprojet/vendor/github.com/FlashSight/go-packages/webserver" // Ou le chemin correct
)

// Handler pour la page d'accueil utilisant RenderPage (avec cache)
func homeHandler(server *webserver.Server) fiber.Handler {
	return func(c *fiber.Ctx) error {
		cacheKey := "home"
		return server.RenderPage(c, cacheKey, webserver.CacheTTLInfo{IsInfinite: false}, func() (templ.Component, int64, error) {
			server.GetLogger().Info("Génération du composant pour la page d'accueil", "cache_key", cacheKey)
			return views.HomePage("Bienvenue !"), time.Now().Unix(), nil
		})
	}
}

// Handler pour un sitemap utilisant RenderBytesPage (avec cache)
func sitemapHandler(server *webserver.Server) fiber.Handler {
	return func(c *fiber.Ctx) error {
		cacheKey := "sitemap.xml"
		contentType := "application/xml; charset=utf-8"
		return server.RenderBytesPage(c, cacheKey, contentType, webserver.CacheTTLInfo{IsInfinite: true}, func() ([]byte, int64, error) {
			server.GetLogger().Info("Génération du sitemap.xml", "cache_key", cacheKey)
			now := time.Now()
			entries := []webserver.SitemapEntry{
				{URL: "https://example.com/", LastMod: &now, ChangeFreq: webserver.SitemapChangeFreqDaily, Priority: 1.0},
				{URL: "https://example.com/about", LastMod: &now, ChangeFreq: webserver.SitemapChangeFreqMonthly, Priority: 0.8},
			}
			xmlBytes, err := webserver.GenerateSitemapXMLBytes(entries)
			return xmlBytes, now.Unix(), err
		})
	}
}

// Handler pour invalider une clé de cache (exemple)
func invalidateCacheHandler(server *webserver.Server) fiber.Handler {
    return func(c *fiber.Ctx) error {
        keyToInvalidate := c.Query("key")
        if keyToInvalidate == "" {
            return c.Status(fiber.StatusBadRequest).SendString("Paramètre 'key' manquant")
        }
        if err := server.Invalidate(keyToInvalidate); err != nil {
            server.GetLogger().Error("Échec de l'invalidation du cache", "key", keyToInvalidate, "error", err)
            return c.Status(fiber.StatusInternalServerError).SendString("Échec de l'invalidation du cache")
        }
        server.GetLogger().Info("Cache invalidé pour la clé", "key", keyToInvalidate)
        return c.SendString(fmt.Sprintf("Cache invalidé pour la clé: %s", keyToInvalidate))
    }
}


// Handler pour une page 404 personnalisée
func notFoundHandler(server *webserver.Server) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if server.GetConfig().ErrorComponentGenerator != nil && !webserver.WantsJSON(c) {
			errorPage := server.GetConfig().ErrorComponentGenerator(fiber.ErrNotFound, fiber.StatusNotFound, server.GetConfig().DevMode)
			if errorPage != nil {
				c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
				return errorPage.Render(c.Context(), c.Response().BodyWriter())
			}
		}
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Page non trouvée"})
	}
}

func main() {
	// --- 1. Logger Setup ---
	logLevel := slog.LevelInfo
	isDev := os.Getenv("APP_ENV") == "development"
	if isDev {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     logLevel,
		AddSource: isDev,
	}))
	slog.SetDefault(logger)

	// --- 2. Configuration ---
	cfg := webserver.Config{
		Port:              webserver.GetEnvOrDefault(logger, "PORT", "", "8080"),
		DevMode:           isDev,
		Logger:            logger,
		SourcesDir:        "./webroot/sources",
		StaticsDir:        "./webroot/statics",
		PublicDir:         "./webroot/public", 
		CacheDir:          "./runtime/cache",  
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		EnableMetrics:     true,
		CacheL1DefaultTTL: 5 * time.Minute,
		CacheL2DefaultTTL: 24 * time.Hour,
		WarmupConcurrency: 4,
		SecurityHeaders: map[string]string{
			"X-Frame-Options": "DENY",
		},
		EnableCSRF:         true, 
		CSRFKeyLookup:      "header:X-CSRF-Token, form:_csrf",
		CSRFCookieName:     "__Host-csrf",
		CSRFExpiration:     12 * time.Hour,
		CSRFCookieSameSite: "Lax",
		EnableRateLimiter:     true, 
		RateLimiterMax:        100,
		RateLimiterExpiration: 1 * time.Minute,
		ErrorComponentGenerator: func(err error, code int, isDevMode bool) templ.Component {
			errMsg := err.Error()
			if fe, ok := err.(*fiber.Error); ok {
				errMsg = fe.Message
			}
			if !isDevMode && code >= 500 {
				errMsg = "Une erreur interne est survenue."
			}
			return views.ErrorPage(fmt.Sprintf("Erreur %d", code), errMsg, isDevMode) // Assurez-vous que views.ErrorPage existe
		},
	}

	// --- 3. Server Initialization ---
	server, err := webserver.NewServer(cfg)
	if err != nil {
		logger.Error("Échec de l'initialisation du webserver", "error", err)
		os.Exit(1)
	}

	app := server.App() 

	// --- 4. Configuration des Middlewares (Exemple: CSRF, Rate Limiter) ---
	if cfg.EnableCSRF {
		app.Use(csrf.New(csrf.Config{
			KeyLookup:      cfg.CSRFKeyLookup,
			CookieName:     cfg.CSRFCookieName,
			CookieSameSite: cfg.CSRFCookieSameSite,
			Expiration:     cfg.CSRFExpiration,
			ContextKey: webserver.CSRFContextKey, 
		}))
	}
	if cfg.EnableRateLimiter {
		app.Use(limiter.New(limiter.Config{
			Max:        cfg.RateLimiterMax,
			Expiration: cfg.RateLimiterExpiration,
			KeyGenerator: func(c *fiber.Ctx) string {
				return webserver.GetClientIP(c.Get(fiber.HeaderXForwardedFor), c.IP())
			},
		}))
	}

	// --- 5. Définir les Routes ---
	app.Get("/", homeHandler(server))
	app.Get("/sitemap.xml", sitemapHandler(server))
    app.Post("/admin/cache/invalidate", invalidateCacheHandler(server)) // Sécuriser cette route ! (ex: avec un middleware d'auth)
	app.Post("/submit", func(c *fiber.Ctx) error {
		return c.SendString("Données soumises avec succès!")
	})


	// --- 6. Enregistrer le Middleware pour les Fichiers Statiques ---
	if cfg.PublicDir != "" {
		app.Static("/", cfg.PublicDir, fiber.Static{
			Compress: true,
		})
	}

	// --- 7. Cache Warmup (Optionnel) ---
	server.RegisterForPageWarmup("home", webserver.CacheTTLInfo{IsInfinite: false}, func() (templ.Component, int64, error) {
		return views.HomePage("Bienvenue ! (Warmup)"), time.Now().Unix(), nil // Assurez-vous que views.HomePage existe
	})
	go func() {
		logger.Info("Démarrage du préchauffage du cache...")
		if err := server.ExecuteWarmup(); err != nil {
			logger.Error("Échec du préchauffage du cache", "error", err)
		} else {
			logger.Info("Préchauffage du cache terminé.")
		}
	}()


	// --- 8. Handler 404 (Doit être le dernier) ---
	app.Use(notFoundHandler(server))


	// --- 9. Start Server & Handle Shutdown ---
	go func() {
		if err := server.Start(); err != nil {
			if err.Error() != "http: Server closed" { 
				logger.Error("Le serveur n'a pas pu démarrer ou s'est arrêté de manière inattendue", "error", err)
			}
		}
	}()
	logger.Info("Serveur démarré", "port", cfg.Port, "dev_mode", cfg.DevMode)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Signal d'arrêt reçu, fermeture du serveur...")
	if err := server.Shutdown(); err != nil {
		logger.Error("Échec de l'arrêt du serveur", "error", err)
		os.Exit(1)
	}
	logger.Info("Arrêt du serveur terminé.")
}

```

---

## 5. Configuration Détaillée

### 5.1 Struct `Config`

| Champ                     | Type                          | YAML/JSON Key                  | Description                                                                                                                                                              | Défaut (si applicable)        | Variable Env (prioritaire) |
| :------------------------ | :---------------------------- | :----------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :---------------------------- | :------------------------- |
| `Port`                    | `string`                      | `port`                         | Port d'écoute.                                                                                                                                                           | `"8080"` (via `NewServer`)    | `PORT`                     |
| `ReadTimeout`             | `time.Duration`               | `read_timeout`                 | Timeout lecture requête.                                                                                                                                                 | `30s` (via `NewServer`)       | -                          |
| `WriteTimeout`            | `time.Duration`               | `write_timeout`                | Timeout écriture réponse.                                                                                                                                                | `30s` (via `NewServer`)       | -                          |
| `IdleTimeout`             | `time.Duration`               | `idle_timeout`                 | Timeout inactivité connexion.                                                                                                                                            | `60s` (via `NewServer`)       | -                          |
| `DevMode`                 | `bool`                        | `dev_mode`                     | Mode développement (logs verbeux, etc.).                                                                                                                                 | `false`                       | `APP_ENV=development`      |
| `Logger`                  | `*slog.Logger`                | `-`                            | Instance `slog.Logger`. Si nil, un logger par défaut est créé.                                                                                                           | `nil` (défaut créé)         | -                          |
| `PublicDir`               | `string`                      | `public_dir`                   | **Requis si service de fichiers statiques désiré.** Chemin vers le répertoire public (après traitement par `StaticProcessor`).                                      | `""`                          | -                          |
| `CacheDir`                | `string`                      | `cache_dir`                    | Chemin pour BadgerDB (L2). Si vide, L2 désactivé.                                                                                                                         | `""`                          | -                          |
| `SourcesDir`              | `string`                      | `sources_dir`                  | Chemin vers sources CSS/JS à minifier.                                                                                                                                   | `""`                          | -                          |
| `StaticsDir`              | `string`                      | `statics_dir`                  | Chemin vers statiques à copier (images, etc.).                                                                                                                          | `""`                          | -                          |
| `ErrorHandler`            | `func(c *fiber.Ctx, err error) error` | `-`                            | Handler d'erreur Fiber personnalisé. Si nil, `server.handleError` est utilisé.                                                                                         | `nil`                         | -                          |
| `NotFoundComponent`       | `templ.Component`             | `-`                            | Composant Templ pour erreurs 404 (utilisé par `handleError` si l'erreur est un 404 et pas de `ErrorComponentGenerator`). Non utilisé par un handler 404 spécifique.   | `nil`                         | -                          |
| `ErrorComponentGenerator` | `ErrorComponentGenerator`     | `-`                            | `func(err error, code int, isDev bool) templ.Component` pour générer pages d'erreur.                                                                                  | `nil`                         | -                          |
| `CacheL1DefaultTTL`       | `time.Duration`               | `cache_l1_default_ttl`         | TTL par défaut L1. `0` ou négatif avec `IsInfinite:false` peut signifier `cache.DefaultExpiration` selon `go-cache`.                                                | `5m` (via `NewServer`)        | `CACHE_L1_DEFAULT_TTL`     |
| `CacheL2DefaultTTL`       | `time.Duration`               | `cache_l2_default_ttl`         | TTL par défaut L2. `0` ou négatif signifie pas d'expiration automatique par TTL pour Badger.                                                                             | `24h` (via `NewServer`)       | `CACHE_L2_DEFAULT_TTL`     |
| `BadgerGCInterval`        | `time.Duration`               | `badger_gc_interval`           | Intervalle GC BadgerDB. `0` ou négatif désactive GC périodique.                                                                                                          | `1h` (via `NewServer`)        | `BADGER_GC_INTERVAL`       |
| `BadgerGCDiscardRatio`    | `float64`                     | `badger_gc_discard_ratio`      | Ratio GC BadgerDB.                                                                                                                                                       | `0.5` (via `NewServer`)       | -                          |
| `WarmupConcurrency`       | `int`                         | `warmup_concurrency`           | Goroutines max pour warmup.                                                                                                                                              | `4` (via `NewServer`)         | -                          |
| `EnableCSRF`              | `bool`                        | `enable_csrf`                  | Flag pour indiquer si CSRF doit être activé (l'utilisateur doit ajouter le middleware).                                                                                 | `false`                       | -                          |
| `CSRFKeyLookup`           | `string`                      | `csrf_key_lookup`              | Source token CSRF (Format Fiber: "header:X-CSRF-Token", "form:_csrf").                                                                                                   | `""`                          | -                          |
| `CSRFCookieName`          | `string`                      | `csrf_cookie_name`             | Nom cookie CSRF.                                                                                                                                                         | `""`                          | -                          |
| `CSRFExpiration`          | `time.Duration`               | `csrf_expiration`              | Durée validité token CSRF.                                                                                                                                               | `0`                           | -                          |
| `CSRFCookieSameSite`      | `string`                      | `csrf_cookie_same_site`        | Politique SameSite cookie CSRF.                                                                                                                                          | `""`                          | -                          |
| `EnableRateLimiter`       | `bool`                        | `enable_rate_limiter`          | Flag pour indiquer si Rate Limiter doit être activé (l'utilisateur doit ajouter le middleware).                                                                        | `false`                       | -                          |
| `RateLimiterMax`          | `int`                         | `rate_limiter_max`             | Requêtes max par fenêtre.                                                                                                                                                | `0`                           | -                          |
| `RateLimiterExpiration`   | `time.Duration`               | `rate_limiter_expiration`      | Durée fenêtre rate limiting.                                                                                                                                             | `0`                           | -                          |
| `SecurityHeaders`         | `map[string]string`           | `security_headers`             | Headers HTTP de sécurité à ajouter (via middleware de base).                                                                                                             | `nil`                         | -                          |
| `EnableMetrics`           | `bool`                        | `enable_metrics`               | Active endpoint Prometheus `/metrics`.                                                                                                                                   | `false`                       | -                          |
| `CustomMiddlewares`       | `[]fiber.Handler`             | `-`                            | Middlewares Fiber personnalisés ajoutés après les middlewares de base.                                                                                                     | `nil`                         | -                          |

### 5.2 Variables d'Environnement Reconnues (par `NewServer` ou `utils`)

*   `PORT`: Surcharge `Config.Port`.
*   `APP_ENV`: Si `"development"`, `Config.DevMode` est `true` par défaut (si `cfg.Logger` est `nil`).
*   `CACHE_L1_DEFAULT_TTL`: Surcharge `Config.CacheL1DefaultTTL` (format `time.ParseDuration`).
*   `CACHE_L2_DEFAULT_TTL`: Surcharge `Config.CacheL2DefaultTTL` (format `time.ParseDuration`).
*   `BADGER_GC_INTERVAL`: Surcharge `Config.BadgerGCInterval` (format `time.ParseDuration`).
*   `CACHE_L1_CLEANUP_INTERVAL`: Intervalle de nettoyage L1 `go-cache` (défaut interne de `NewCache` est `10m`).
*   `CORS_ALLOW_ORIGINS`: Origines autorisées pour CORS (utilisé par le middleware CORS de base).

### 5.3 Validation au Démarrage (`NewServer`)
*   Valide et résout les chemins `PublicDir`, `CacheDir`, `SourcesDir`, `StaticsDir`.
*   Vérifie l'accessibilité en écriture de `CacheDir` (si configuré).
*   Assure l'existence des répertoires configurés.

---

## 6. API de Référence Détaillée

### 6.1 Initialisation et Cycle de Vie

*   **`webserver.Init()`**
    *   Description : Fonction d'initialisation globale (utilise `sync.Once`). Appelée par `NewServer`. (Peu d'impact visible.)
*   **`webserver.NewServer(cfg Config) (*Server, error)`**
    *   Description : Constructeur principal. Valide config, init logger, cache, Fiber, `StaticProcessor`, middlewares de base, monitoring.
    *   Retourne : `*Server`, `error`.
*   **`(*Server) App() *fiber.App`**
    *   Description : Retourne l'instance `*fiber.App` sous-jacente pour enregistrer des routes, middlewares, etc.
*   **`(*Server) Start() error`**
    *   Description : Lance le serveur Fiber. Bloquant.
*   **`(*Server) Shutdown() error`**
    *   Description : Arrêt gracieux. Ferme Fiber et le cache L2.

### 6.2 Routage et Middlewares
Le routage et l'ajout de middlewares spécifiques (comme CSRF, Rate Limiter) se font directement sur l'instance `server.App()` en utilisant les méthodes standard de Fiber (ex: `app.Get()`, `app.Post()`, `app.Use()`).
Le package fournit `webserver.CSRFContextKey` (`"csrf"`) qui peut être utilisé avec le middleware CSRF de Fiber pour accéder au token dans `c.Locals()`.

### 6.3 Rendu et Cache (Méthodes sur `*Server`)

*   **`(*Server) RenderPage(ctx *fiber.Ctx, key string, ttlInfo CacheTTLInfo, generatorFunc PageGeneratorFunc) error`**
    *   Description : Rend un `templ.Component` via le cache L1/L2. Gère lookup, génération, stockage. Définit `Content-Type: text/html`, `X-Cache-Status`.
*   **`(*Server) RenderBytesPage(ctx *fiber.Ctx, key string, contentType string, ttlInfo CacheTTLInfo, generatorFunc BytesGeneratorFunc) error`**
    *   Description : Rend des `[]byte` (XML, JSON, etc.) via le cache L1/L2. Définit `Content-Type`, `X-Cache-Status`.

### 6.4 Gestion Manuelle du Cache (Méthodes sur `*Server`)

*   **`(*Server) Invalidate(key string) error`**: Supprime une clé des caches L1 et L2.
*   **`(*Server) Flush() error`**: **Destructif !** Vide L1 et L2 (`DropAll`).
*   **`(*Server) RegisterForPageWarmup(key string, ttlInfo CacheTTLInfo, generator PageGeneratorFunc)`**: Enregistre un générateur de page Templ pour le préchauffage.
*   **`(*Server) RegisterForBytesWarmup(key string, ttlInfo CacheTTLInfo, generator BytesGeneratorFunc)`**: Enregistre un générateur de bytes pour le préchauffage.
*   **`(*Server) ExecuteWarmup() error`**: Exécute le préchauffage pour tous les items enregistrés. S'exécute en parallèle (`WarmupConcurrency`).

### 6.5 Sitemap

*   **`SitemapEntry` (struct)**: Définit une entrée de sitemap (`URL`, `LastMod`, `ChangeFreq`, `Priority`).
*   **Constantes `SitemapChangeFreq*`**: Valeurs standard pour `ChangeFreq` (`always`, `hourly`, etc.).
*   **`GenerateSitemapXMLBytes(entries []SitemapEntry) ([]byte, error)`**: Génère le XML du sitemap à partir d'une liste d'entrées.

### 6.6 Utilitaires (`webserver.*` et méthodes `*Server`)

*   **`(*Server) GetLogger() *slog.Logger`**: Retourne le logger configuré.
*   **`(*Server) GetConfig() Config`**: Retourne la configuration effective du serveur.
*   **`webserver.GetClientIP(xForwardedFor, remoteAddr string) string`**: Extrait l'IP client.
*   **`webserver.WantsJSON(c *fiber.Ctx) bool`**: Vérifie si le client demande du JSON via l'en-tête `Accept`.
*   **`webserver.GetEnvOrDefault(logger *slog.Logger, key, configValue, defaultValue string) string`**: Récupère une variable d'environnement ou utilise des valeurs de repli.
*   Autres fonctions dans `utils.go` pour la gestion des répertoires, valeurs par défaut, parsing de durées.

---

## 7. Processus Internes Détaillés

### 7.1 Workflow d'une Requête avec `RenderPage`

1.  Requête HTTP -> Fiber -> Middlewares de base (`recover`, `cors`, `logRequests`, `security_headers`, `custom_middlewares` de `Config`).
2.  Puis, les middlewares configurés par l'utilisateur (ex: CSRF, Rate Limiter).
3.  Routage Fiber (`app.Get`, etc.) -> Handler applicatif (`func(c *fiber.Ctx) error`).
4.  Handler appelle `server.RenderPage(c, key, ttlInfo, generatorFunc)`.
5.  **Lookup L1**: `server.Cache.L1.Get(key)`
    *   **Hit**: Incrémente métrique L1 hit, récupère `[]byte`, set `X-Cache-Status: HIT-L1`, set `Content-Type`, `c.Send(bytes)`. Fin.
    *   **Miss**: Incrémente métrique L1 miss. Continue.
6.  **Lookup L2**: `server.Cache.L2.View(...)` pour lire `CacheEntry`.
    *   **Hit & Valide**: Incrémente L2 hit, décode JSON, **Promotion L1**: `server.Cache.L1.Set(...)` (incrémente `loaded_from_l2`), set `X-Cache-Status: HIT-L2`, set `Content-Type`, `c.Send(cacheEntry.Data)`. Fin.
    *   **Miss/Expiré/Erreur L2**: Incrémente L2 miss. Continue.
7.  **Génération**: Appelle `generatorFunc()`. Si `PageGeneratorFunc`, rend le `component` en `[]byte`. Mesure durée (observe métrique). Gère erreurs.
8.  **Stockage Cache**:
    *   **L1**: `server.Cache.L1.Set(...)` (incrémente L1 set).
    *   **L2**: `server.storeInL2(...)` (encode `CacheEntry` en JSON, set TTL BadgerDB si applicable, écrit, incrémente L2 set/error).
9.  **Envoi Réponse**: Set `X-Cache-Status: MISS`, set `Content-Type`, `c.Send(generatedBytes)`.

(Workflow `RenderBytesPage` similaire mais travaille directement avec les `[]byte` du générateur.)

---

## 8. Middleware

### 8.1 Middlewares de Base (Automatiques)
Lors de `NewServer`, les middlewares suivants sont configurés sur l'instance Fiber via `setupBaseMiddlewares`:
*   `recover.New()`: Pour la récupération après panic. Stack trace en `DevMode`.
*   `cors.New()`: Gestion CORS. `AllowOrigins` est configurable via la variable d'environnement `CORS_ALLOW_ORIGINS`. Comportement strict en production.
*   Headers de Sécurité : Si `Config.SecurityHeaders` est fourni, un middleware est ajouté pour les appliquer.
*   `logRequests`: Journalisation structurée de chaque requête.
*   `Config.CustomMiddlewares`: Tout handler `fiber.Handler` fourni dans cette slice est ajouté.

### 8.2 Middlewares Optionnels (Configuration Manuelle)
*   **CSRF**: Si `Config.EnableCSRF` est `true`, vous devez manuellement ajouter le middleware CSRF de Fiber à votre application :
    ```go
    if cfg.EnableCSRF {
        app.Use(csrf.New(csrf.Config{
            KeyLookup:      cfg.CSRFKeyLookup,
            CookieName:     cfg.CSRFCookieName,
            // ... autres options de cfg ...
            ContextKey:     webserver.CSRFContextKey, // Important
        }))
    }
    ```
    Le `webserver.CSRFContextKey` (`"csrf"`) peut être utilisé pour récupérer le token via `c.Locals(webserver.CSRFContextKey)` (utile pour l'injecter dans les formulaires).
*   **Rate Limiter**: Si `Config.EnableRateLimiter` est `true`, ajoutez manuellement le middleware Limiter de Fiber :
    ```go
    if cfg.EnableRateLimiter {
        app.Use(limiter.New(limiter.Config{
            Max:        cfg.RateLimiterMax,
            Expiration: cfg.RateLimiterExpiration,
            KeyGenerator: func(c *fiber.Ctx) string { // Exemple
                return webserver.GetClientIP(c.Get(fiber.HeaderXForwardedFor), c.IP())
            },
            // ...
        }))
    }
    ```

---

## 9. Sécurité

*   **Headers de Sécurité**: Configurez `Config.SecurityHeaders` pour des en-têtes comme `X-Frame-Options`, `Strict-Transport-Security`, `Content-Security-Policy`, etc.
*   **CORS**: Configurez `CORS_ALLOW_ORIGINS` de manière restrictive en production.
*   **CSRF**: Si activé manuellement, assurez-vous que votre frontend envoie le token correctement (`CSRFKeyLookup`). Utilisez des noms de cookie sécurisés (`__Host-` si HTTPS).
*   **Rate Limiting**: Si activé manuellement, ajustez les seuils (`RateLimiterMax`, `RateLimiterExpiration`) à vos besoins.
*   **Gestion des Erreurs**: Assurez-vous que `Config.DevMode` est `false` en production pour ne pas exposer de détails d'erreur sensibles.
*   **Dépendances**: Maintenez les dépendances à jour, en particulier Fiber et BadgerDB.
*   **Invalidation de Cache**: Les méthodes `Invalidate` et `Flush` sont puissantes. Si vous les exposez via des endpoints HTTP (comme dans l'exemple `invalidateCacheHandler`), assurez-vous que ces endpoints sont correctement sécurisés (authentification, autorisation).

---

## 10. Monitoring et Observabilité

### 10.1 Métriques Prometheus
*   **Activation**: `Config.EnableMetrics: true`.
*   **Endpoint**: `/metrics` (exposé par `ansrivas/fiberprometheus/v2`).
*   **Métriques Spécifiques au Webserver**:
    *   `webserver_cache_l1_hits_total`
    *   `webserver_cache_l1_misses_total`
    *   `webserver_cache_l1_sets_total`
    *   `webserver_cache_l1_loaded_from_l2_total`
    *   `webserver_cache_l2_hits_total`
    *   `webserver_cache_l2_misses_total`
    *   `webserver_cache_l2_sets_total`
    *   `webserver_cache_l2_set_errors_total`
    *   `webserver_cache_invalidations_total`
    *   `webserver_cache_invalidation_errors_total`
    *   `webserver_cache_warmup_skipped_total`
    *   `webserver_cache_warmup_errors_total`
    *   `webserver_cache_warmup_item_duration_seconds` (Histogram)
    *   `webserver_cache_warmup_total_duration_seconds` (Gauge)
    *   `webserver_page_generation_duration_seconds` (HistogramVec, label: `cache_key`)
*   **Métriques Fiber**: Latence, requêtes par statut/méthode, etc. (fournies par `fiberprometheus`).

### 10.2 Health Check
*   **Endpoint**: `GET /health`.
*   **Logique**: Répond HTTP 200 si le serveur est en cours d'exécution. Si le cache L2 (BadgerDB) est configuré, il tente une lecture simple pour vérifier son accessibilité.
*   **Réponses**:
    *   `200 OK`: `{"status": "ok", "l2_cache": "ok"}` (si L2 OK)
    *   `200 OK`: `{"status": "ok", "l2_cache": "unavailable", "l2_cache_error": "L2 Cache (BadgerDB) is not configured/initialized"}` (si L2 non configuré)
    *   `503 Service Unavailable`: `{"status": "error", "l2_cache": "unhealthy", "l2_cache_error": "<badger error details>"}` (si lecture L2 échoue).

### 10.3 Logging (`slog`)
*   Utilisez le logger `slog` configuré (accessible via `server.GetLogger()`).
*   Le middleware `logRequests` fournit des logs détaillés et structurés pour chaque requête.

---

## 11. Gestion des Erreurs

*   L'`ErrorHandler` par défaut (`server.handleError`) est configuré pour l'instance Fiber. Il est appelé quand un handler retourne une erreur ou via `c.Next(err)`.
*   Il détermine le code HTTP et le message à partir de l'erreur (priorité à `*fiber.Error`).
*   Logue l'erreur avec des détails contextuels.
*   **Format de Réponse**:
    *   Si `webserver.WantsJSON(c)` est vrai : Réponse JSON `{"error": "message"}`.
    *   Sinon, et si `Config.ErrorComponentGenerator` est fourni : Tente de rendre le composant Templ généré.
    *   Sinon (fallback) : Réponse `text/plain` `<code>: <message>`.
    *   En `DevMode`, les messages d'erreur peuvent être plus détaillés. En production, les erreurs 5xx affichent "Internal Server Error".
*   Vous pouvez fournir un `ErrorHandler` entièrement personnalisé dans `Config.ErrorHandler` ou simplement un `Config.ErrorComponentGenerator` pour personnaliser l'affichage HTML des erreurs.

---

## 12. Bonnes Pratiques et Conseils

*   **Handlers Fiber**: Utilisez la signature standard `func(c *fiber.Ctx) error`.
*   **Clés de Cache**: Choisissez des clés uniques et significatives.
*   **`CacheTTLInfo`**: Utilisez `IsInfinite: true` pour les contenus stables (sitemap, etc.) dont l'invalidation est manuelle.
*   **Generator Functions**: Concentrez-les sur la récupération de données et la création du contenu.
*   **Warmup Sélectif**: Ne préchauffez que les ressources critiques pour éviter une charge excessive au démarrage.
*   **Configuration**: Utilisez des variables d'environnement pour les déploiements.
*   **Ordre des Middlewares/Handlers**: L'ordre est crucial dans Fiber. Typiquement, le handler 404 et `app.Static` viennent en dernier.
*   **Sécurité des Endpoints d'Invalidation**: Si vous créez des endpoints HTTP pour appeler `server.Invalidate()` ou `server.Flush()`, **sécurisez-les impérativement** (authentification, autorisation forte).

---

## 13. Troubleshooting

*   **Erreurs "Directory not writable"**: Vérifiez les permissions de `CacheDir` et `PublicDir`.
*   **Assets non trouvés (404)**:
    *   Vérifiez que `app.Static("/", cfg.PublicDir)` est correctement configuré et appelé *après* les routes spécifiques qui pourraient avoir des conflits de préfixe.
    *   Vérifiez le contenu de `PublicDir` après le démarrage.
*   **Cache non invalidé / contenu périmé**:
    *   Vérifiez les TTLs (`CacheL1DefaultTTL`, `CacheL2DefaultTTL`) et `IsInfinite`.
    *   Vérifiez les logs pour les erreurs de cache L2 (métrique `webserver_cache_l2_set_errors_total`).
*   **Problèmes CSRF (si activé manuellement)**:
    *   Vérifiez la configuration du middleware CSRF.
    *   Assurez-vous que le frontend envoie le token et que `KeyLookup` correspond.
    *   Inspectez les cookies et headers.
*   **Erreurs 503 / Health Check échoue**: Vérifiez les logs pour des erreurs BadgerDB. `CacheDir` doit être accessible.

---

## 14. Roadmap Potentielle (Idées d'Évolution)

*   Cache busting automatique pour les assets.
*   Options de TTL de cache plus granulaires.
*   Intégration OpenTelemetry.
*   Health check personnalisable par l'application.
*   Améliorations du `StaticProcessor` (ex: ne pas purger `PublicDir`, support SASS/TS).

---

## 15. Glossaire

*   **L1 Cache**: Cache en mémoire (`go-cache`).
*   **L2 Cache**: Cache sur disque (BadgerDB).
*   **Generator Function**: Fonction (`PageGeneratorFunc` ou `BytesGeneratorFunc`) produisant le contenu pour le cache.
*   **RenderPage / RenderBytesPage**: Méthodes pour rendre du contenu avec cache.
*   **Warmup**: Pré-remplissage du cache.
*   **StaticProcessor**: Composant traitant les assets statiques au démarrage.
*   **slog**: Librairie de logging structuré de Go.
*   **Fiber**: Framework web Go.
*   **Templ**: Moteur de template Go (supporté via `templ.Component`).