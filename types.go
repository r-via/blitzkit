// File: types.go
// Description: Définit les types de données et interfaces utilisés par le package blitzkit,
//
//	y compris la structure de configuration `Config`, les types de fonctions
//	pour les générateurs de cache, et les types liés au préchauffage du cache.
package blitzkit

import (
	"log/slog"
	"time"

	"github.com/a-h/templ"
	"github.com/gofiber/fiber/v2"
)

// Config regroupe tous les paramètres de configuration du serveur web.
type Config struct {
	// Port: Le port TCP sur lequel le serveur écoute (ex: "8080").
	Port string `json:"port" yaml:"port"`
	// ReadTimeout: Durée maximale pour lire l'intégralité de la requête, y compris le corps.
	ReadTimeout time.Duration `json:"read_timeout" yaml:"read_timeout"`
	// WriteTimeout: Durée maximale pour écrire la réponse.
	WriteTimeout time.Duration `json:"write_timeout" yaml:"write_timeout"`
	// IdleTimeout: Durée maximale d'attente pour la prochaine requête sur une connexion persistante.
	IdleTimeout time.Duration `json:"idle_timeout" yaml:"idle_timeout"`
	// DevMode: Active le mode développement (logs plus verbeux, pas de prefork, etc.).
	DevMode bool `json:"dev_mode" yaml:"dev_mode"`
	// Logger: L'instance du logger slog à utiliser par le serveur.
	Logger *slog.Logger `json:"-" yaml:"-"`
	// PublicDir: Chemin absolu du répertoire contenant les fichiers statiques publics servis par app.Static.
	PublicDir string `json:"public_dir" yaml:"public_dir"`
	// CacheDir: Chemin absolu du répertoire pour le cache L2 (BadgerDB). Si vide, L2 est désactivé.
	CacheDir string `json:"cache_dir" yaml:"cache_dir"`
	// SourcesDir: Chemin du répertoire contenant les sources CSS/JS à minifier.
	SourcesDir string `json:"sources_dir" yaml:"sources_dir"`
	// StaticsDir: Chemin du répertoire contenant les fichiers statiques à copier directement dans PublicDir.
	StaticsDir string `json:"statics_dir" yaml:"statics_dir"`
	// ErrorHandler: Fonction personnalisée pour gérer les erreurs Fiber. Si nil, utilise le gestionnaire par défaut.
	ErrorHandler func(c *fiber.Ctx, err error) error `json:"-" yaml:"-"`
	// NotFoundComponent: Composant Templ à afficher pour les erreurs 404 (non implémenté actuellement dans handleError).
	NotFoundComponent templ.Component `json:"-" yaml:"-"`
	// ErrorComponentGenerator: Fonction qui génère un composant Templ pour afficher une page d'erreur.
	ErrorComponentGenerator ErrorComponentGenerator `json:"-" yaml:"-"`
	// CacheL1DefaultTTL: Durée de vie par défaut pour les éléments dans le cache L1 (mémoire). `0` ou négatif signifie pas d'expiration automatique (utilisé avec `IsInfinite`).
	CacheL1DefaultTTL time.Duration `json:"cache_l1_default_ttl" yaml:"cache_l1_default_ttl"`
	// CacheL2DefaultTTL: Durée de vie par défaut pour les éléments dans le cache L2 (BadgerDB). `0` ou négatif signifie pas d'expiration automatique.
	CacheL2DefaultTTL time.Duration `json:"cache_l2_default_ttl" yaml:"cache_l2_default_ttl"`
	// BadgerGCInterval: Intervalle pour le déclenchement du Garbage Collector de BadgerDB. `0` ou négatif désactive le GC périodique.
	BadgerGCInterval time.Duration `json:"badger_gc_interval" yaml:"badger_gc_interval"`
	// BadgerGCDiscardRatio: Ratio utilisé par le GC de BadgerDB (généralement 0.5).
	BadgerGCDiscardRatio float64 `json:"badger_gc_discard_ratio" yaml:"badger_gc_discard_ratio"`
	// WarmupConcurrency: Nombre maximum de goroutines pour exécuter le préchauffage du cache en parallèle.
	WarmupConcurrency int `json:"warmup_concurrency" yaml:"warmup_concurrency"`
	// EnableCSRF: Active ou désactive la protection CSRF via middleware.
	EnableCSRF bool `json:"enable_csrf" yaml:"enable_csrf"`
	// CSRFKeyLookup: Source où chercher le jeton CSRF dans la requête (ex: "header:X-CSRF-Token"). Voir la documentation Fiber CSRF.
	CSRFKeyLookup string `json:"csrf_key_lookup" yaml:"csrf_key_lookup"`
	// CSRFCookieName: Nom du cookie utilisé pour stocker le secret CSRF.
	CSRFCookieName string `json:"csrf_cookie_name" yaml:"csrf_cookie_name"`
	// CSRFExpiration: Durée de validité du jeton CSRF.
	CSRFExpiration time.Duration `json:"csrf_expiration" yaml:"csrf_expiration"`
	// CSRFCookieSameSite: Politique SameSite pour le cookie CSRF ("Lax", "Strict", "None").
	CSRFCookieSameSite string `json:"csrf_cookie_same_site" yaml:"csrf_cookie_same_site"`
	// EnableRateLimiter: Active ou désactive le middleware de limitation de débit.
	EnableRateLimiter bool `json:"enable_rate_limiter" yaml:"enable_rate_limiter"`
	// RateLimiterMax: Nombre maximum de requêtes autorisées par fenêtre de temps.
	RateLimiterMax int `json:"rate_limiter_max" yaml:"rate_limiter_max"`
	// RateLimiterExpiration: Durée de la fenêtre de temps pour la limitation de débit.
	RateLimiterExpiration time.Duration `json:"rate_limiter_expiration" yaml:"rate_limiter_expiration"`
	// SecurityHeaders: Map des en-têtes HTTP de sécurité à ajouter à chaque réponse.
	SecurityHeaders map[string]string `json:"security_headers" yaml:"security_headers"`
	// EnableMetrics: Active ou désactive l'exposition des métriques Prometheus via /metrics.
	EnableMetrics bool `json:"enable_metrics" yaml:"enable_metrics"`
	// CustomMiddlewares: Slice de middlewares Fiber personnalisés à ajouter à la chaîne globale.
	CustomMiddlewares []fiber.Handler `json:"-" yaml:"-"`
}

// BytesGeneratorFunc définit la signature d'une fonction utilisée pour générer
// des données binaires (ex: sitemap XML) pour le cache.
// Doit retourner les données, un timestamp Unix de dernière modification, et une erreur éventuelle.
type BytesGeneratorFunc func() (data []byte, lastModified int64, err error)

// PageGeneratorFunc définit la signature d'une fonction utilisée pour générer
// une page HTML (via un composant Templ) pour le cache.
// Doit retourner le composant, un timestamp Unix de dernière modification, et une erreur éventuelle.
type PageGeneratorFunc func() (page templ.Component, lastModified int64, err error)

// CacheTTLInfo spécifie si une entrée de cache doit avoir une durée de vie infinie.
type CacheTTLInfo struct {
	IsInfinite bool
}

// WarmupRegistration contient les informations nécessaires pour enregistrer un élément
// pour le préchauffage du cache (clé, fonction de génération, type, TTL).
type WarmupRegistration struct {
	Key           string
	GeneratorFunc interface{} // Doit être casté en BytesGeneratorFunc ou PageGeneratorFunc
	IsBytes       bool
	TTLInfo       CacheTTLInfo
}

// ErrorComponentGenerator définit la signature d'une fonction qui génère un composant Templ
// pour afficher une page d'erreur basée sur l'erreur, le code HTTP et le mode de développement.
type ErrorComponentGenerator func(err error, code int, isDev bool) templ.Component

// CSRFContextKey est la clé utilisée pour stocker/récupérer le jeton CSRF dans les locaux du contexte Fiber.
const CSRFContextKey = "csrf"
