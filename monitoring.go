// File: pkg/blitzkit/monitoring.go
// Description: Gère la configuration et l'exposition des métriques Prometheus
//
//	et du point de terminaison de contrôle de santé (/health).
package blitzkit

import (
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/dgraph-io/badger/v4"
	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// --- Prometheus Metrics Definitions ---

// cacheL1Hits: Compteur total des succès du cache L1.
var cacheL1Hits prometheus.Counter

// cacheL1Misses: Compteur total des échecs du cache L1.
var cacheL1Misses prometheus.Counter

// cacheL1Sets: Compteur total des éléments définis dans le cache L1.
var cacheL1Sets prometheus.Counter

// cacheL1LoadedFromL2: Compteur total des éléments chargés dans L1 depuis L2.
var cacheL1LoadedFromL2 prometheus.Counter

// cacheL2Hits: Compteur total des succès du cache L2.
var cacheL2Hits prometheus.Counter

// cacheL2Misses: Compteur total des échecs du cache L2 (inclut non trouvé, erreurs, etc.).
var cacheL2Misses prometheus.Counter

// cacheL2Sets: Compteur total des éléments définis avec succès dans le cache L2.
var cacheL2Sets prometheus.Counter

// cacheL2SetErrors: Compteur total des erreurs lors de la définition d'éléments dans L2.
var cacheL2SetErrors prometheus.Counter

// cacheInvalidations: Compteur total des invalidations de cache réussies.
var cacheInvalidations prometheus.Counter

// cacheInvalidationErrors: Compteur total des erreurs lors de l'invalidation du cache.
var cacheInvalidationErrors prometheus.Counter

// cacheWarmupSkipped: Compteur total des éléments sautés lors du préchauffage du cache (déjà dans L1).
var cacheWarmupSkipped prometheus.Counter

// cacheWarmupErrors: Compteur total des erreurs lors de la génération/stockage du préchauffage.
var cacheWarmupErrors prometheus.Counter

// cacheWarmupDuration: Histogramme de la durée de préchauffage pour chaque élément individuel.
var cacheWarmupDuration prometheus.Histogram

// cacheWarmupTotalDuration: Jauge de la durée totale du dernier processus de préchauffage.
var cacheWarmupTotalDuration prometheus.Gauge

// pageGenerationDuration: Histogramme (vector) de la durée de génération de page/contenu en cas de cache miss, labellisé par clé de cache.
var pageGenerationDuration *prometheus.HistogramVec

// metricsInitialized assure que l'initialisation des métriques ne se produit qu'une seule fois.
var metricsInitialized atomic.Bool

// setupMonitoring configure les points de terminaison Prometheus et de contrôle de santé.
// Appelé en interne par NewServer.
//
// Args:
//
//	app (*fiber.App): L'instance de l'application Fiber.
//	cfg (Config): La configuration du serveur web.
//	logger (*slog.Logger): Le logger structuré.
//	db (*badger.DB): La connexion à la base de données BadgerDB (L2), utilisée pour le health check (peut être nil).
func setupMonitoring(app *fiber.App, cfg Config, logger *slog.Logger, db *badger.DB) {
	if cfg.EnableMetrics {
		initMetrics(logger)

		fiberProm := fiberprometheus.New("blitzkit_app")
		fiberProm.RegisterAt(app, "/metrics")
		app.Use(fiberProm.Middleware)
		logger.Info("Prometheus metrics enabled", "path", "/metrics")
	}

	app.Get("/health", handleHealthCheck(db, logger))
	logger.Info("Health check endpoint enabled", "path", "/health")
}

// initMetrics initialise les collecteurs Prometheus pour les métriques personnalisées.
// Utilise `atomic.Bool` pour garantir une initialisation unique (thread-safe).
//
// Args:
//
//	logger (*slog.Logger): Le logger pour enregistrer le statut d'initialisation.
func initMetrics(logger *slog.Logger) {
	if metricsInitialized.CompareAndSwap(false, true) {
		logger.Debug("Initializing Prometheus metrics collectors...")
		cacheL1Hits = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_l1_hits_total", Help: "Total L1 cache hits."})
		cacheL1Misses = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_l1_misses_total", Help: "Total L1 cache misses."})
		cacheL1Sets = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_l1_sets_total", Help: "Total items set in L1 cache."})
		cacheL1LoadedFromL2 = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_l1_loaded_from_l2_total", Help: "Total items loaded into L1 from L2."})
		cacheL2Hits = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_l2_hits_total", Help: "Total L2 cache hits."})
		cacheL2Misses = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_l2_misses_total", Help: "Total L2 cache misses (incl. not found, unmarshal errors, db errors)."})
		cacheL2Sets = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_l2_sets_total", Help: "Total items successfully set in L2 cache."})
		cacheL2SetErrors = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_l2_set_errors_total", Help: "Total errors setting items in L2 cache."})
		cacheInvalidations = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_invalidations_total", Help: "Total successful cache invalidations."})
		cacheInvalidationErrors = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_invalidation_errors_total", Help: "Total errors during cache invalidation."})
		cacheWarmupSkipped = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_warmup_skipped_total", Help: "Total items skipped during cache warmup (already in L1)."})
		cacheWarmupErrors = promauto.NewCounter(prometheus.CounterOpts{Name: "blitzkit_cache_warmup_errors_total", Help: "Total errors during cache warmup generation/render/store."})
		cacheWarmupDuration = promauto.NewHistogram(prometheus.HistogramOpts{Name: "blitzkit_cache_warmup_item_duration_seconds", Help: "Duration to warm up individual cache items.", Buckets: prometheus.DefBuckets})
		cacheWarmupTotalDuration = promauto.NewGauge(prometheus.GaugeOpts{Name: "blitzkit_cache_warmup_total_duration_seconds", Help: "Total duration of the last cache warmup process."})
		pageGenerationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{Name: "blitzkit_page_generation_duration_seconds", Help: "Duration to generate page/byte content (cache miss).", Buckets: prometheus.DefBuckets}, []string{"cache_key"})

		logger.Debug("Prometheus metrics collectors initialized.")
	} else {
		logger.Debug("Prometheus metrics collectors already initialized.")
	}
}

// IncCacheL1Hit incrémente le compteur de succès L1 si les métriques sont initialisées.
func IncCacheL1Hit() {
	if metricsInitialized.Load() {
		cacheL1Hits.Inc()
	}
}

// IncCacheL1Miss incrémente le compteur d'échecs L1 si les métriques sont initialisées.
func IncCacheL1Miss() {
	if metricsInitialized.Load() {
		cacheL1Misses.Inc()
	}
}

// IncCacheL1Set incrémente le compteur d'éléments définis en L1 si les métriques sont initialisées.
func IncCacheL1Set() {
	if metricsInitialized.Load() {
		cacheL1Sets.Inc()
	}
}

// IncCacheL1LoadedFromL2 incrémente le compteur d'éléments L1 chargés depuis L2 si les métriques sont initialisées.
func IncCacheL1LoadedFromL2() {
	if metricsInitialized.Load() {
		cacheL1LoadedFromL2.Inc()
	}
}

// IncCacheL2Hit incrémente le compteur de succès L2 si les métriques sont initialisées.
func IncCacheL2Hit() {
	if metricsInitialized.Load() {
		cacheL2Hits.Inc()
	}
}

// IncCacheL2Miss incrémente le compteur d'échecs L2 si les métriques sont initialisées.
func IncCacheL2Miss() {
	if metricsInitialized.Load() {
		cacheL2Misses.Inc()
	}
}

// IncCacheL2Set incrémente le compteur d'éléments définis en L2 si les métriques sont initialisées.
func IncCacheL2Set() {
	if metricsInitialized.Load() {
		cacheL2Sets.Inc()
	}
}

// IncCacheL2SetErrors incrémente le compteur d'erreurs de définition L2 si les métriques sont initialisées.
func IncCacheL2SetErrors() {
	if metricsInitialized.Load() {
		cacheL2SetErrors.Inc()
	}
}

// IncCacheInvalidations incrémente le compteur d'invalidations réussies si les métriques sont initialisées.
func IncCacheInvalidations() {
	if metricsInitialized.Load() {
		cacheInvalidations.Inc()
	}
}

// IncCacheInvalidationErrors incrémente le compteur d'erreurs d'invalidation si les métriques sont initialisées.
func IncCacheInvalidationErrors() {
	if metricsInitialized.Load() {
		cacheInvalidationErrors.Inc()
	}
}

// IncCacheWarmupSkipped incrémente le compteur d'éléments sautés lors du préchauffage si les métriques sont initialisées.
func IncCacheWarmupSkipped() {
	if metricsInitialized.Load() {
		cacheWarmupSkipped.Inc()
	}
}

// IncCacheWarmupErrors incrémente le compteur d'erreurs de préchauffage si les métriques sont initialisées.
func IncCacheWarmupErrors() {
	if metricsInitialized.Load() {
		cacheWarmupErrors.Inc()
	}
}

// ObserveCacheWarmupDuration enregistre la durée de préchauffage d'un élément individuel si les métriques sont initialisées.
func ObserveCacheWarmupDuration(duration float64) {
	if metricsInitialized.Load() {
		cacheWarmupDuration.Observe(duration)
	}
}

// ObserveCacheWarmupTotalDuration définit la durée totale du dernier préchauffage si les métriques sont initialisées.
func ObserveCacheWarmupTotalDuration(duration float64) {
	if metricsInitialized.Load() {
		cacheWarmupTotalDuration.Set(duration)
	}
}

// ObservePageGenerationDuration enregistre la durée de génération d'une page (cache miss) si les métriques sont initialisées.
func ObservePageGenerationDuration(duration float64, cacheKey string) {
	if metricsInitialized.Load() {
		pageGenerationDuration.WithLabelValues(cacheKey).Observe(duration)
	}
}

// handleHealthCheck retourne un handler Fiber pour le point de terminaison /health.
// Ce handler vérifie le statut des composants critiques (actuellement, le cache L2 BadgerDB via une lecture rapide)
// et retourne une réponse JSON indiquant le statut global ("ok" ou "error") et le statut de chaque composant vérifié.
// Retourne un statut HTTP 200 si tout est OK, ou 503 Service Unavailable si un composant critique est défaillant.
//
// Args:
//
//	db (*badger.DB): La connexion à la base de données BadgerDB (peut être nil si L2 désactivé).
//	logger (*slog.Logger): Le logger structuré.
//
// Returns:
//
//	fiber.Handler: Le handler Fiber pour la route /health.
func handleHealthCheck(db *badger.DB, logger *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		dbStatus := "ok"
		dbErr := ""
		overallStatus := "ok"
		httpStatus := fiber.StatusOK

		if db == nil {
			dbStatus = "unavailable"
			dbErr = "L2 Cache (BadgerDB) is not configured/initialized"
			logger.Warn("Health check: L2 cache DB is nil")
		} else {
			err := db.View(func(txn *badger.Txn) error {
				_, errGet := txn.Get([]byte("health_check_probe"))
				if errGet != nil && !errors.Is(errGet, badger.ErrKeyNotFound) {
					return errGet
				}
				return nil
			})

			if err != nil {
				dbStatus = "unhealthy"
				dbErr = fmt.Sprintf("L2 Cache read check failed: %v", err)
				logger.Error("Health check: L2 Cache read failed", "error", err)
				overallStatus = "error"
				httpStatus = fiber.StatusServiceUnavailable
			} else {
				dbStatus = "ok"
			}
		}

		response := fiber.Map{
			"status":   overallStatus,
			"l2_cache": dbStatus,
		}
		if dbErr != "" {
			response["l2_cache_error"] = dbErr
		}

		return c.Status(httpStatus).JSON(response)
	}
}
