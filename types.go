// Package blitzkit provides a core server toolkit for building web applications with Go and Fiber.
// It includes features for configuration, caching, static file processing, error handling,
// middleware, monitoring, and sitemap generation.
package blitzkit

import (
	"log/slog"
	"time"

	"github.com/a-h/templ"
	"github.com/gofiber/fiber/v2"
)

// HeaderCacheStatus is the HTTP header name used to indicate cache status (HIT/MISS).
const HeaderCacheStatus = "X-BlitzKit-Cache-Status" // MODIFIÉ: Renommé pour être plus spécifique à BlitzKit et exporté

// Config holds all configuration parameters for the BlitzKit web server.
type Config struct {
	// AppName is the name of the application, used in Fiber config.
	AppName string `json:"app_name" yaml:"app_name"` // AJOUTÉ
	// Port is the TCP port the server will listen on (e.g., "8080").
	Port string `json:"port" yaml:"port"`
	// ReadTimeout is the maximum duration for reading the entire request, including the body.
	ReadTimeout time.Duration `json:"read_timeout" yaml:"read_timeout"`
	// WriteTimeout is the maximum duration for writing the response.
	WriteTimeout time.Duration `json:"write_timeout" yaml:"write_timeout"`
	// IdleTimeout is the maximum duration to wait for the next request on a persistent connection.
	IdleTimeout time.Duration `json:"idle_timeout" yaml:"idle_timeout"`
	// DevMode enables development mode features (more verbose logging, no prefork, etc.).
	DevMode bool `json:"dev_mode" yaml:"dev_mode"`
	// MinifyCSS controls whether CSS files from SourcesDir are minified.
	MinifyCSS bool `json:"minify_css" yaml:"minify_css"`
	// MinifyJS controls whether JavaScript files from SourcesDir are minified.
	MinifyJS bool `json:"minify_js" yaml:"minify_js"`
	// UseEsbuildIfAvailable controls whether esbuild is used for JS minification if JS_MINIFY is true and esbuild is found.
	UseEsbuildIfAvailable bool `json:"use_esbuild_if_available" yaml:"use_esbuild_if_available"`
	// Logger is the slog.Logger instance to be used by the server. If nil, a default one is created.
	Logger *slog.Logger `json:"-" yaml:"-"`
	// PublicDir is the absolute path to the directory containing public static files served by app.Static.
	PublicDir string `json:"public_dir" yaml:"public_dir"`
	// CacheDir is the absolute path to the directory for L2 cache (BadgerDB). If empty, L2 cache is disabled.
	CacheDir string `json:"cache_dir" yaml:"cache_dir"`
	// SourcesDir is the path to the directory containing CSS/JS sources to be minified.
	SourcesDir string `json:"sources_dir" yaml:"sources_dir"`
	// StaticsDir is the path to the directory containing static files to be copied directly to PublicDir.
	StaticsDir string `json:"statics_dir" yaml:"statics_dir"`
	// ErrorHandler is a custom function to handle Fiber errors. If nil, a default handler is used.
	ErrorHandler func(c *fiber.Ctx, err error) error `json:"-" yaml:"-"`
	// ErrorComponentGenerator is a function that generates a templ.Component to display an error page.
	ErrorComponentGenerator ErrorComponentGenerator `json:"-" yaml:"-"`
	// CacheL1DefaultTTL is the default time-to-live for items in the L1 (memory) cache.
	CacheL1DefaultTTL time.Duration `json:"cache_l1_default_ttl" yaml:"cache_l1_default_ttl"`
	// CacheL2DefaultTTL is the default time-to-live for items in the L2 (BadgerDB) cache.
	CacheL2DefaultTTL time.Duration `json:"cache_l2_default_ttl" yaml:"cache_l2_default_ttl"`
	// BadgerGCInterval is the interval for triggering BadgerDB's Garbage Collector.
	BadgerGCInterval time.Duration `json:"badger_gc_interval" yaml:"badger_gc_interval"`
	// BadgerGCDiscardRatio is the ratio used by BadgerDB's GC (typically 0.5).
	BadgerGCDiscardRatio float64 `json:"badger_gc_discard_ratio" yaml:"badger_gc_discard_ratio"`
	// WarmupConcurrency is the maximum number of goroutines for concurrent cache warmup.
	WarmupConcurrency int `json:"warmup_concurrency" yaml:"warmup_concurrency"`
	// EnableCSRF enables or disables CSRF protection middleware.
	EnableCSRF bool `json:"enable_csrf" yaml:"enable_csrf"`
	// CSRFKeyLookup specifies where to look for the CSRF token in requests (e.g., "header:X-CSRF-Token").
	CSRFKeyLookup string `json:"csrf_key_lookup" yaml:"csrf_key_lookup"`
	// CSRFCookieName is the name of the cookie used to store the CSRF secret.
	CSRFCookieName string `json:"csrf_cookie_name" yaml:"csrf_cookie_name"`
	// CSRFExpiration is the duration for which the CSRF token is valid.
	CSRFExpiration time.Duration `json:"csrf_expiration" yaml:"csrf_expiration"`
	// CSRFCookieSameSite is the SameSite policy for the CSRF cookie ("Lax", "Strict", "None").
	CSRFCookieSameSite string `json:"csrf_cookie_same_site" yaml:"csrf_cookie_same_site"`
	// EnableRateLimiter enables or disables the rate limiting middleware.
	EnableRateLimiter bool `json:"enable_rate_limiter" yaml:"enable_rate_limiter"`
	// RateLimiterMax is the maximum number of requests allowed per time window for rate limiting.
	RateLimiterMax int `json:"rate_limiter_max" yaml:"rate_limiter_max"`
	// RateLimiterExpiration is the duration of the time window for rate limiting.
	RateLimiterExpiration time.Duration `json:"rate_limiter_expiration" yaml:"rate_limiter_expiration"`
	// SecurityHeaders is a map of HTTP security headers to be added to every response.
	SecurityHeaders map[string]string `json:"security_headers" yaml:"security_headers"`
	// EnableMetrics enables or disables exposing Prometheus metrics via /metrics.
	EnableMetrics bool `json:"enable_metrics" yaml:"enable_metrics"`
	// CustomMiddlewares is a slice of custom Fiber middlewares to be added to the global chain.
	CustomMiddlewares []fiber.Handler `json:"-" yaml:"-"`
}

// BytesGeneratorFunc defines the signature for a function that generates binary data (e.g., sitemap XML) for caching.
// It must return the data, a Unix timestamp of the last modification, and an optional error.
type BytesGeneratorFunc func() (data []byte, lastModified int64, err error)

// PageGeneratorFunc defines the signature for a function that generates an HTML page (via a templ.Component) for caching.
// It must return the component, a Unix timestamp of the last modification, and an optional error.
type PageGeneratorFunc func() (page templ.Component, lastModified int64, err error)

// CacheTTLInfo specifies whether a cache entry should have an infinite time-to-live.
type CacheTTLInfo struct {
	IsInfinite bool
}

// WarmupRegistration holds information needed to register an item for cache warmup.
// This includes the cache key, the generator function, its type (bytes or page), and TTL info.
type WarmupRegistration struct {
	Key           string
	GeneratorFunc interface{}
	IsBytes       bool
	TTLInfo       CacheTTLInfo
}

// ErrorComponentGenerator defines the signature for a function that generates a templ.Component
// to display an error page, based on the error, HTTP status code, and development mode status.
type ErrorComponentGenerator func(err error, code int, isDev bool) templ.Component

// CSRFContextKey is the key used for storing/retrieving the CSRF token in Fiber context locals.
const CSRFContextKey = "csrf"
