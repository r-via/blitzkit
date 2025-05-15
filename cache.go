// File: cache.go
// Description: Gère la mise en cache à deux niveaux (L1 en mémoire, L2 sur disque via BadgerDB).
//
//	Inclut l'initialisation, la fermeture, la gestion du cycle de vie (GC)
//	et l'adaptation du logger BadgerDB à slog.
package blitzkit

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	cache "github.com/patrickmn/go-cache"
)

// CacheEntry définit la structure stockée dans le cache L1/L2.
// Contient les données binaires, l'heure de dernière modification et l'heure d'expiration.
type CacheEntry struct {
	Data         []byte `json:"data"`
	LastModified int64  `json:"last_modified"`
	ExpiresAt    int64  `json:"expires_at"`
}

// Cache contient les pointeurs vers les caches L1 (en mémoire) et L2 (BadgerDB),
// ainsi que les éléments de contrôle pour le garbage collector (GC) de BadgerDB.
type Cache struct {
	L1 *cache.Cache
	L2 *badger.DB

	stopGC chan struct{}
	gcWG   sync.WaitGroup
}

// badgerSlogAdapter adapte slog.Logger à l'interface logger de BadgerDB.
type badgerSlogAdapter struct {
	logger *slog.Logger
}

// NewCache initialise les systèmes de cache L1 (go-cache) et L2 (BadgerDB).
// Configure les intervalles de nettoyage/GC et démarre la goroutine GC pour BadgerDB si activée.
// Retourne une erreur si l'initialisation de BadgerDB échoue.
//
// Args:
//
//	cacheDir (string): Le chemin du répertoire pour stocker la base de données BadgerDB (L2). Si vide, L2 est désactivé.
//	logger (*slog.Logger): Le logger structuré à utiliser.
//	cleanupInterval (time.Duration): L'intervalle de nettoyage pour le cache L1 (go-cache).
//	gcInterval (time.Duration): L'intervalle pour exécuter le GC du journal de valeurs de BadgerDB (L2). Si <= 0, le GC périodique est désactivé.
//	discardRatio (float64): Le ratio pour le GC de BadgerDB (généralement 0.5).
//
// Returns:
//
//	(*Cache, error): Une instance de Cache initialisée et une erreur nil, ou nil et une erreur en cas d'échec.
func NewCache(
	cacheDir string,
	logger *slog.Logger,
	cleanupInterval time.Duration,
	gcInterval time.Duration,
	discardRatio float64,
) (*Cache, error) {
	if logger == nil {
		logger = slog.Default()
		logger.Warn("NewCache using default logger.")
	}
	logger.Debug("Initializing caches...")

	if cleanupInterval <= 0 {
		cleanupInterval = 10 * time.Minute
	}
	l1 := cache.New(cache.NoExpiration, cleanupInterval)
	logger.Debug("L1 cache initialized.", "cleanupInterval", cleanupInterval)

	var l2 *badger.DB
	var stopGCChan chan struct{}
	cacheInstance := &Cache{L1: l1, L2: nil}

	if cacheDir != "" {
		logger.Debug("Attempting L2 cache init", "path", cacheDir)
		opts := badger.DefaultOptions(cacheDir).
			WithLoggingLevel(badger.WARNING).
			WithLogger(&badgerSlogAdapter{logger: logger})

		var errL2 error
		l2, errL2 = badger.Open(opts)
		if errL2 != nil {
			logger.Error("Failed to open BadgerDB for L2 cache, L2 disabled.", "path", cacheDir, "error", errL2)
			l2 = nil
		} else {
			logger.Info("L2 cache (BadgerDB) initialized", "path", cacheDir)
			cacheInstance.L2 = l2

			if gcInterval > 0 {
				logger.Info("Starting periodic BadgerDB GC", "interval", gcInterval, "discard_ratio", discardRatio)
				stopGCChan = make(chan struct{})
				cacheInstance.stopGC = stopGCChan
				cacheInstance.gcWG.Add(1)
				go runBadgerGC(l2, logger, stopGCChan, gcInterval, discardRatio, &cacheInstance.gcWG)
			} else {
				logger.Info("Periodic BadgerDB GC disabled by configuration.")
			}
		}
	} else {
		logger.Warn("CacheDir not configured, L2 cache disabled.")
	}

	return cacheInstance, nil
}

// Close arrête proprement la goroutine GC de BadgerDB (si elle existe)
// et ferme la connexion à la base de données BadgerDB (L2).
//
// Args:
//
//	logger (*slog.Logger): Le logger structuré à utiliser.
//
// Returns:
//
//	error: Une erreur si la fermeture de BadgerDB échoue, sinon nil.
func (c *Cache) Close(logger *slog.Logger) error {
	log := logger
	if log == nil {
		log = slog.Default()
	}

	if c.stopGC != nil {
		log.Info("Stopping BadgerDB GC goroutine...")
		select {
		case <-c.stopGC:
			log.Warn("BadgerDB GC stop channel already closed.")
		default:
			close(c.stopGC)
		}
		c.gcWG.Wait()
		log.Info("BadgerDB GC goroutine stopped.")
		c.stopGC = nil
	} else {
		log.Debug("BadgerDB GC goroutine was not running.")
	}

	if c.L2 == nil {
		log.Debug("L2 cache was nil, skipping close.")
		return nil
	}
	log.Debug("Closing L2 cache (BadgerDB)...")
	err := c.L2.Close()
	if err != nil {
		log.Error("Failed to close BadgerDB", "error", err)
		return fmt.Errorf("failed to close L2 cache: %w", err)
	}
	log.Info("L2 cache closed successfully.")
	return nil
}

// Errorf implémente la méthode Errorf de l'interface badger.Logger.
// Transmet le message formaté au logger slog sous-jacent au niveau Error.
func (bsa *badgerSlogAdapter) Errorf(format string, args ...interface{}) {
	if bsa.logger != nil {
		bsa.logger.Error(fmt.Sprintf(format, args...))
	}
}

// Warningf implémente la méthode Warningf de l'interface badger.Logger.
// Transmet le message formaté au logger slog sous-jacent au niveau Warn.
func (bsa *badgerSlogAdapter) Warningf(format string, args ...interface{}) {
	if bsa.logger != nil {
		bsa.logger.Warn(fmt.Sprintf(format, args...))
	}
}

// Infof implémente la méthode Infof de l'interface badger.Logger.
// Transmet le message formaté au logger slog sous-jacent au niveau Debug (car Badger Info est souvent verbeux).
func (bsa *badgerSlogAdapter) Infof(format string, args ...interface{}) {
	if bsa.logger != nil {
		bsa.logger.Debug(fmt.Sprintf(format, args...))
	}
}

// Debugf implémente la méthode Debugf de l'interface badger.Logger.
// Transmet le message formaté au logger slog sous-jacent au niveau Debug.
func (bsa *badgerSlogAdapter) Debugf(format string, args ...interface{}) {
	if bsa.logger != nil {
		bsa.logger.Debug(fmt.Sprintf(format, args...))
	}
}

// runBadgerGC est la fonction exécutée par la goroutine de garbage collection de BadgerDB.
// Elle exécute périodiquement `db.RunValueLogGC` à l'intervalle spécifié et s'arrête
// lorsque le canal `stopChan` est fermé.
//
// Args:
//
//	db (*badger.DB): La connexion à la base de données BadgerDB sur laquelle exécuter le GC.
//	logger (*slog.Logger): Le logger pour enregistrer les événements du GC.
//	stopChan (chan struct{}): Le canal utilisé pour signaler l'arrêt de la goroutine.
//	gcInterval (time.Duration): L'intervalle entre les exécutions du GC.
//	discardRatio (float64): Le ratio de discard pour le GC.
//	wg (*sync.WaitGroup): Le WaitGroup à notifier lorsque la goroutine se termine.
func runBadgerGC(db *badger.DB, logger *slog.Logger, stopChan chan struct{}, gcInterval time.Duration, discardRatio float64, wg *sync.WaitGroup) {
	defer wg.Done()

	if db == nil || logger == nil || stopChan == nil || wg == nil {
		println("FATAL: nil parameter(s) passed to runBadgerGC")
		return
	}

	ticker := time.NewTicker(gcInterval)
	defer ticker.Stop()

	logger.Debug("BadgerDB GC goroutine started", "interval", gcInterval, "ratio", discardRatio)

	for {
		select {
		case <-ticker.C:
			logCtx := logger.With(slog.Float64("discard_ratio", discardRatio))
			logCtx.Info("Running periodic BadgerDB Value Log GC task...")
			startTime := time.Now()
			runCount := 0

			for {
				runCount++
				gcErr := db.RunValueLogGC(discardRatio)
				if errors.Is(gcErr, badger.ErrNoRewrite) {
					logCtx.Info("No rewrite needed during BadgerDB GC", "iterations", runCount)
					break
				}
				if gcErr != nil {
					logCtx.Error("Error during BadgerDB RunValueLogGC iteration", "iteration", runCount, "error", gcErr)
					break
				}
			}

			duration := time.Since(startTime)
			logCtx.Info("BadgerDB GC task completed", "duration", duration, "iterations", runCount)

		case <-stopChan:
			logger.Info("BadgerDB GC goroutine received stop signal. Exiting.")
			return
		}
	}
}
