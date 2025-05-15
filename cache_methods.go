// File: pkg/webserver/cache_methods.go
// Description: Contient les méthodes du serveur web pour interagir avec le système de cache.
//
//	Inclut le rendu de pages HTML (templ) et de données binaires via le cache,
//	l'invalidation et le vidage du cache.
package webserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/gofiber/fiber/v2"
	cache "github.com/patrickmn/go-cache"
)

// RenderPage effectue le rendu d'une page HTML (générée par une fonction `PageGeneratorFunc`)
// en utilisant le système de cache L1/L2.
// 1. Tente de récupérer depuis L1.
// 2. Si absent ou invalide, tente de récupérer depuis L2.
// 3. Si L2 trouvé et valide, le charge dans L1 et sert le contenu.
// 4. Si L2 absent ou expiré, appelle `generatorFunc`, stocke le résultat dans L1 et L2, et sert le contenu généré.
// Définit l'en-tête `X-Cache-Status` (HIT-L1, HIT-L2, MISS) et `Content-Type`.
//
// Args:
//
//	ctx (interface{}): Le contexte de la requête, attendu comme *fiber.Ctx.
//	key (string): La clé unique identifiant cette page dans le cache.
//	ttlInfo (CacheTTLInfo): Informations sur la durée de vie du cache (infini ou par défaut).
//	generatorFunc (PageGeneratorFunc): La fonction qui génère le composant `templ.Component` et le timestamp lastModified si le cache est manquant.
//
// Returns:
//
//	error: Une erreur si le type de contexte est invalide, si la génération échoue,
//	       si le rendu échoue, ou si l'envoi de la réponse échoue. Gérée par l'ErrorHandler de Fiber.
func (s *Server) RenderPage(ctx interface{}, key string, ttlInfo CacheTTLInfo, generatorFunc PageGeneratorFunc) error {
	c, ok := ctx.(*fiber.Ctx)
	if !ok {
		s.logger.Error("Invalid context type for RenderPage", "expected", "*fiber.Ctx", "received", fmt.Sprintf("%T", ctx))
		return fiber.NewError(http.StatusInternalServerError, "Internal server error (invalid context type in RenderPage)")
	}
	logCtx := s.logger.With(slog.String("cache_key", key), slog.String("path", c.Path()))

	if s.Cache != nil && s.Cache.L1 != nil {
		if cachedItem, found := s.Cache.L1.Get(key); found {
			IncCacheL1Hit()
			if dataBytes, ok := cachedItem.([]byte); ok {
				if s.config.DevMode {
					logCtx.Debug("Cache L1 hit")
				}
				c.Set(HeaderCacheStatus, "HIT-L1")
				c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
				return c.Send(dataBytes)
			}
			logCtx.Warn("Cache L1 hit but invalid data type found, deleting", "type", fmt.Sprintf("%T", cachedItem))
			s.Cache.L1.Delete(key)
		} else {
			IncCacheL1Miss()
			if s.config.DevMode {
				logCtx.Debug("Cache L1 miss")
			}
		}
	} else {
		logCtx.Warn("L1 cache not available for RenderPage lookup")
	}

	var cacheEntry CacheEntry
	foundInL2 := false
	if s.Cache != nil && s.Cache.L2 != nil {
		errL2 := s.Cache.L2.View(func(txn *badger.Txn) error {
			item, err := txn.Get([]byte(key))
			if err != nil {
				return err
			}
			return item.Value(func(val []byte) error {
				if err := json.Unmarshal(val, &cacheEntry); err != nil {
					logCtx.Warn("Failed to unmarshal L2 cache entry, treating as miss", "error", err)
					return nil
				}
				foundInL2 = true
				return nil
			})
		})
		if errL2 != nil && !errors.Is(errL2, badger.ErrKeyNotFound) {
			logCtx.Error("Error during L2 cache View", "error", errL2)
			foundInL2 = false
			IncCacheL2Miss()
		} else if errors.Is(errL2, badger.ErrKeyNotFound) {
			IncCacheL2Miss()
		} else if foundInL2 {
			IncCacheL2Hit()
		}
	} else {
		if s.config.DevMode {
			logCtx.Debug("L2 Cache not available for RenderPage lookup")
		}
	}

	now := time.Now().Unix()
	if foundInL2 {
		isExpired := cacheEntry.ExpiresAt != 0 && cacheEntry.ExpiresAt <= now
		if !isExpired {
			if s.config.DevMode {
				logCtx.Debug("Cache L2 hit (valid)")
			}
			if s.Cache != nil && s.Cache.L1 != nil {
				l1TTL := s.config.CacheL1DefaultTTL
				if ttlInfo.IsInfinite {
					l1TTL = cache.NoExpiration
				}
				s.Cache.L1.Set(key, cacheEntry.Data, l1TTL)
				IncCacheL1LoadedFromL2()
				if s.config.DevMode {
					logCtx.Debug("Loaded L2 content into L1", "l1_ttl", l1TTL)
				}
			}
			c.Set(HeaderCacheStatus, "HIT-L2")
			c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
			return c.Send(cacheEntry.Data)
		}
		if s.config.DevMode {
			logCtx.Debug("Cache L2 hit but expired", "expires_at", time.Unix(cacheEntry.ExpiresAt, 0))
		}
	} else if s.Cache != nil && s.Cache.L2 != nil {
		if s.config.DevMode {
			logCtx.Debug("Cache L2 miss or entry invalid/expired")
		}
	}

	if generatorFunc == nil {
		logCtx.Error("Page generator function is nil")
		return fiber.NewError(http.StatusInternalServerError, fmt.Sprintf("nil generator for key %s", key))
	}

	if s.config.DevMode {
		logCtx.Debug("Generating page content")
	}
	generationStart := time.Now()
	component, lastModified, errGen := generatorFunc()
	generationDuration := time.Since(generationStart)
	ObservePageGenerationDuration(generationDuration.Seconds(), key)

	if errGen != nil {
		var fe *fiber.Error
		if errors.As(errGen, &fe) {
			logCtx.Warn("Generator returned fiber error", "status", fe.Code, "error", fe.Message)
			return fe
		}
		logCtx.Error("Page generator failed", "error", errGen, "duration", generationDuration)
		return fiber.NewError(http.StatusInternalServerError, fmt.Sprintf("page generator failed for key %s", key))
	}
	if component == nil {
		logCtx.Error("Page generator returned nil component")
		return fiber.NewError(http.StatusInternalServerError, fmt.Sprintf("generator returned nil component for %s", key))
	}

	var buf bytes.Buffer
	if errRender := component.Render(c.Context(), &buf); errRender != nil {
		logCtx.Error("Failed to render templ component after generation", "error", errRender)
		return fiber.NewError(http.StatusInternalServerError, "Failed to render generated component")
	}
	generatedBytes := buf.Bytes()

	if s.Cache != nil {
		if s.Cache.L1 != nil {
			l1TTL := s.config.CacheL1DefaultTTL
			if ttlInfo.IsInfinite {
				l1TTL = cache.NoExpiration
			}
			s.Cache.L1.Set(key, generatedBytes, l1TTL)
			IncCacheL1Set()
			if s.config.DevMode {
				logCtx.Debug("Stored generated content in L1", "l1_ttl", l1TTL)
			}
		}
		if s.Cache.L2 != nil {
			errStore := s.storeInL2(key, CacheEntry{Data: generatedBytes, LastModified: lastModified}, ttlInfo.IsInfinite)
			if errStore != nil {
				logCtx.Error("Failed to store generated content in L2", "error", errStore)
			}
		}
	}

	c.Set(HeaderCacheStatus, "MISS")
	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	return c.Send(generatedBytes)
}

// RenderBytesPage effectue le rendu de données binaires (ex: XML, CSS, JS)
// (générées par une fonction `BytesGeneratorFunc`) en utilisant le système de cache L1/L2.
// La logique est similaire à `RenderPage`, mais travaille directement avec des slices de bytes.
// Définit l'en-tête `X-Cache-Status` et le `Content-Type` fourni.
//
// Args:
//
//	ctx (interface{}): Le contexte de la requête, attendu comme *fiber.Ctx.
//	key (string): La clé unique identifiant ces données dans le cache.
//	contentType (string): Le type MIME à définir dans l'en-tête Content-Type de la réponse.
//	ttlInfo (CacheTTLInfo): Informations sur la durée de vie du cache (infini ou par défaut).
//	generatorFunc (BytesGeneratorFunc): La fonction qui génère la slice de bytes et le timestamp lastModified si le cache est manquant.
//
// Returns:
//
//	error: Une erreur si le type de contexte est invalide, si la génération échoue,
//	       ou si l'envoi de la réponse échoue. Gérée par l'ErrorHandler de Fiber.
func (s *Server) RenderBytesPage(ctx interface{}, key string, contentType string, ttlInfo CacheTTLInfo, generatorFunc BytesGeneratorFunc) error {
	c, ok := ctx.(*fiber.Ctx)
	if !ok {
		s.logger.Error("Invalid context type for RenderBytesPage", "expected", "*fiber.Ctx", "received", fmt.Sprintf("%T", ctx))
		return fiber.NewError(http.StatusInternalServerError, "Internal server error (invalid context type in RenderBytesPage)")
	}
	logCtx := s.logger.With(slog.String("cache_key", key), slog.String("path", c.Path()), slog.String("content_type", contentType))

	if s.Cache != nil && s.Cache.L1 != nil {
		if cachedItem, found := s.Cache.L1.Get(key); found {
			IncCacheL1Hit()
			if dataBytes, ok := cachedItem.([]byte); ok {
				if s.config.DevMode {
					logCtx.Debug("Cache L1 hit (bytes)")
				}
				c.Set(HeaderCacheStatus, "HIT-L1")
				c.Set(fiber.HeaderContentType, contentType)
				return c.Send(dataBytes)
			}
			logCtx.Warn("Cache L1 hit (bytes) but invalid data type found, deleting", "type", fmt.Sprintf("%T", cachedItem))
			s.Cache.L1.Delete(key)
		} else {
			IncCacheL1Miss()
			if s.config.DevMode {
				logCtx.Debug("Cache L1 miss (bytes)")
			}
		}
	} else {
		logCtx.Warn("L1 cache not available for RenderBytesPage lookup")
	}

	var cacheEntry CacheEntry
	foundInL2 := false
	if s.Cache != nil && s.Cache.L2 != nil {
		errL2 := s.Cache.L2.View(func(txn *badger.Txn) error {
			item, err := txn.Get([]byte(key))
			if err != nil {
				return err
			}
			return item.Value(func(val []byte) error {
				if err := json.Unmarshal(val, &cacheEntry); err != nil {
					logCtx.Warn("Failed L2 unmarshal (bytes)", "error", err)
					return nil
				}
				foundInL2 = true
				return nil
			})
		})
		if errL2 != nil && !errors.Is(errL2, badger.ErrKeyNotFound) {
			logCtx.Error("Error during L2 cache View (bytes)", "error", errL2)
			foundInL2 = false
			IncCacheL2Miss()
		} else if errors.Is(errL2, badger.ErrKeyNotFound) {
			IncCacheL2Miss()
		} else if foundInL2 {
			IncCacheL2Hit()
		}
	} else {
		if s.config.DevMode {
			logCtx.Debug("L2 Cache not available for RenderBytesPage lookup")
		}
	}

	now := time.Now().Unix()
	if foundInL2 {
		isExpired := cacheEntry.ExpiresAt != 0 && cacheEntry.ExpiresAt <= now
		if !isExpired {
			if s.config.DevMode {
				logCtx.Debug("Cache L2 hit (valid) (bytes)")
			}
			if s.Cache != nil && s.Cache.L1 != nil {
				l1TTL := s.config.CacheL1DefaultTTL
				if ttlInfo.IsInfinite {
					l1TTL = cache.NoExpiration
				}
				s.Cache.L1.Set(key, cacheEntry.Data, l1TTL)
				IncCacheL1LoadedFromL2()
				if s.config.DevMode {
					logCtx.Debug("Loaded L2 bytes into L1", "l1_ttl", l1TTL)
				}
			}
			c.Set(HeaderCacheStatus, "HIT-L2")
			c.Set(fiber.HeaderContentType, contentType)
			return c.Send(cacheEntry.Data)
		}
		if s.config.DevMode {
			logCtx.Debug("Cache L2 hit but expired (bytes)")
		}
	} else if s.Cache != nil && s.Cache.L2 != nil {
		if s.config.DevMode {
			logCtx.Debug("Cache L2 miss or entry invalid/expired (bytes)")
		}
	}

	if generatorFunc == nil {
		logCtx.Error("Byte generator function is nil")
		return fiber.NewError(http.StatusInternalServerError, fmt.Sprintf("nil generator for key %s", key))
	}

	generationStart := time.Now()
	generatedBytes, lastModified, errGen := generatorFunc()
	generationDuration := time.Since(generationStart)
	ObservePageGenerationDuration(generationDuration.Seconds(), key)
	if errGen != nil {
		logCtx.Error("Byte generator function failed", "error", errGen, "duration", generationDuration)
		return fiber.NewError(http.StatusInternalServerError, fmt.Sprintf("byte generator failed for key %s", key))
	}
	if generatedBytes == nil {
		logCtx.Error("Byte generator returned nil data")
		return fiber.NewError(http.StatusInternalServerError, fmt.Sprintf("generator returned nil data for %s", key))
	}

	if s.Cache != nil {
		if s.Cache.L1 != nil {
			l1TTL := s.config.CacheL1DefaultTTL
			if ttlInfo.IsInfinite {
				l1TTL = cache.NoExpiration
			}
			s.Cache.L1.Set(key, generatedBytes, l1TTL)
			IncCacheL1Set()
			if s.config.DevMode {
				logCtx.Debug("Stored generated bytes in L1", "l1_ttl", l1TTL)
			}
		}
		if s.Cache.L2 != nil {
			errStore := s.storeInL2(key, CacheEntry{Data: generatedBytes, LastModified: lastModified}, ttlInfo.IsInfinite)
			if errStore != nil {
				logCtx.Error("Failed to store generated bytes in L2 cache", "error", errStore)
			}
		}
	}

	c.Set(HeaderCacheStatus, "MISS")
	c.Set(fiber.HeaderContentType, contentType)
	return c.Send(generatedBytes)
}

// Invalidate supprime une clé spécifique des caches L1 et L2.
// Logue les informations sur l'opération et les éventuelles erreurs de suppression L2.
// Incrémente les compteurs Prometheus correspondants.
//
// Args:
//
//	key (string): La clé de cache à invalider.
//
// Returns:
//
//	error: Retourne la première erreur rencontrée lors de la suppression L2, ou nil si l'opération réussit.
func (s *Server) Invalidate(key string) error {
	if s.Cache == nil {
		s.logger.Warn("Invalidate called but cache is nil", slog.String("cache_key", key))
		return nil
	}
	logCtx := s.logger.With(slog.String("cache_key", key))
	logCtx.Info("Invalidating cache key")
	var firstError error

	if s.Cache.L1 != nil {
		s.Cache.L1.Delete(key)
		if s.config.DevMode {
			logCtx.Debug("Removed key from L1 cache")
		}
	}

	if s.Cache.L2 != nil {
		errL2 := s.Cache.L2.Update(func(txn *badger.Txn) error {
			err := txn.Delete([]byte(key))
			if err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
				IncCacheInvalidationErrors()
				return fmt.Errorf("L2 delete failed: %w", err)
			}
			return nil
		})
		if errL2 != nil {
			logCtx.Error("L2 Update operation failed during invalidation", "error", errL2)
			if firstError == nil {
				firstError = errL2
			}
		} else {
			if s.config.DevMode {
				logCtx.Debug("L2 key removed or not found")
			}
		}
	}
	if firstError == nil {
		IncCacheInvalidations()
		if s.config.DevMode {
			logCtx.Debug("Cache invalidation successful or key not found")
		}
	}
	return firstError
}

// Flush vide complètement les caches L1 (mémoire) et L2 (disque - BadgerDB DropAll).
// Cette opération est destructive et supprime toutes les données mises en cache.
// Logue les informations sur l'opération et les éventuelles erreurs.
//
// Returns:
//
//	error: Retourne la première erreur rencontrée lors du vidage L2, ou nil si l'opération réussit.
func (s *Server) Flush() error {
	if s.Cache == nil {
		s.logger.Warn("Flush called but cache is nil")
		return nil
	}
	s.logger.Warn("Flushing ALL caches (L1 and L2)...")
	var firstError error

	if s.Cache.L1 != nil {
		s.Cache.L1.Flush()
		s.logger.Info("Flushed L1 cache.")
	}

	if s.Cache.L2 != nil {
		s.logger.Info("Dropping all data from L2 cache (BadgerDB)...")
		errL2 := s.Cache.L2.DropAll()
		if errL2 != nil {
			s.logger.Error("Failed to flush L2 cache (BadgerDB DropAll)", "error", errL2)
			if firstError == nil {
				firstError = errL2
			}
		} else {
			s.logger.Info("Flushed L2 cache (BadgerDB) successfully.")
		}
	}
	if firstError == nil {
		s.logger.Info("Cache flush completed.")
	} else {
		s.logger.Error("Cache flush completed with errors.", "error", firstError)
	}
	return firstError
}

// storeInL2 stocke une entrée `CacheEntry` dans le cache L2 (BadgerDB).
// L'entrée est d'abord sérialisée en JSON. Le TTL BadgerDB est défini si l'entrée
// n'est pas marquée comme infinie et si le TTL L2 par défaut est positif.
// Logue les erreurs de sérialisation ou de stockage et incrémente les compteurs Prometheus.
//
// Args:
//
//	key (string): La clé sous laquelle stocker l'entrée.
//	entry (CacheEntry): L'entrée de cache à stocker.
//	isInfinite (bool): Indique si l'entrée doit expirer (selon CacheL2DefaultTTL) ou non.
//
// Returns:
//
//	error: Une erreur si la sérialisation JSON ou l'opération BadgerDB `Update` échoue, sinon nil.
func (s *Server) storeInL2(key string, entry CacheEntry, isInfinite bool) error {
	if s.Cache == nil || s.Cache.L2 == nil {
		if s.config.DevMode {
			s.logger.Debug("L2 cache not available, skipping store", slog.String("cache_key", key))
		}
		return nil
	}
	logCtx := s.logger.With(slog.String("cache_key", key))

	var l2TTL time.Duration
	entry.ExpiresAt = 0
	if !isInfinite {
		l2TTL = s.config.CacheL2DefaultTTL
		if l2TTL > 0 {
			entry.ExpiresAt = time.Now().Add(l2TTL).Unix()
		} else {
			isInfinite = true
		}
	}
	if s.config.DevMode {
		logCtx.Debug("Storing in L2", "isInfinite", isInfinite, "expiresAt", entry.ExpiresAt)
	}

	cacheData, err := json.Marshal(entry)
	if err != nil {
		logCtx.Error("Failed marshal cache entry for L2", "error", err)
		IncCacheL2SetErrors()
		return fmt.Errorf("failed marshal entry for %s: %w", key, err)
	}

	err = s.Cache.L2.Update(func(txn *badger.Txn) error {
		badgerEntry := badger.NewEntry([]byte(key), cacheData)
		if !isInfinite && l2TTL > 0 {
			badgerEntry = badgerEntry.WithTTL(l2TTL)
		}
		return txn.SetEntry(badgerEntry)
	})
	if err != nil {
		IncCacheL2SetErrors()
		logCtx.Error("Failed store in L2", "error", err)
		return fmt.Errorf("failed L2 Update for %s: %w", key, err)
	}

	IncCacheL2Set()
	if s.config.DevMode {
		logCtx.Debug("Stored entry in L2 successfully")
	}
	return nil
}
