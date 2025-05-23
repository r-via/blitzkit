// Package blitzkit provides a core server toolkit for building web applications with Go and Fiber.
package blitzkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/dgraph-io/badger/v4"
	"github.com/gofiber/fiber/v2"
	cache "github.com/patrickmn/go-cache"
)

var initOnce sync.Once

// Server encapsulates the Fiber application, configuration, logger, cache system,
// and cache warmup registry.
type Server struct {
	app            *fiber.App
	config         Config
	logger         *slog.Logger
	Cache          *Cache
	warmupRegistry []WarmupRegistration
	warmupMutex    sync.Mutex
}

// Init performs one-time initialization for the blitzkit package.
func Init() {
	initOnce.Do(func() {
		slog.Debug("blitzkit package initialized (initOnce)")
	})
}

// NewServer creates and configures a new web server instance.
// It validates the provided configuration, initializes the logger (if not provided),
// sets up the L1/L2 cache system, creates the Fiber app instance, processes static files,
// applies base middlewares, and configures monitoring (metrics, health check).
// The behavior of static file minification (CSS, JS) and esbuild usage is determined by
// the Config.MinifyCSS, Config.MinifyJS, and Config.UseEsbuildIfAvailable fields, which
// can be influenced by environment variables (CSS_MINIFY, JS_MINIFY, USE_ESBUILD_IF_AVAILABLE).
func NewServer(cfg Config) (*Server, error) {
	Init()
	var validationErrors []string

	logger := cfg.Logger
	if logger == nil {
		logLevel := slog.LevelInfo
		if cfg.DevMode {
			logLevel = slog.LevelDebug
		}
		opts := &slog.HandlerOptions{Level: logLevel, AddSource: cfg.DevMode}
		logger = slog.New(slog.NewTextHandler(os.Stderr, opts))
		logger.Info("No logger provided, created default slog logger", "level", logLevel.String(), "dev_mode", cfg.DevMode)
	}
	cfg.Logger = logger

	cfg.MinifyCSS = getEnvAsBool(logger, "CSS_MINIFY", !cfg.DevMode)
	cfg.MinifyJS = getEnvAsBool(logger, "JS_MINIFY", !cfg.DevMode)
	cfg.UseEsbuildIfAvailable = getEnvAsBool(logger, "USE_ESBUILD_IF_AVAILABLE", !cfg.DevMode)
	cfg.AppName = getEnvOrDefault(logger, "APP_NAME", cfg.AppName, "BlitzKitApp") // AJOUTÉ: Initialisation de AppName

	if cfg.PublicDir != "" {
		abs, err := filepath.Abs(cfg.PublicDir)
		if err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("PublicDir '%s' invalid path: %v", cfg.PublicDir, err))
		} else {
			cfg.PublicDir = abs
		}
		if err := ensureDirExists(cfg.PublicDir, logger); err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("PublicDir creation failed: %v", err))
		}
	} else {
		logger.Warn("PublicDir is not configured. Serving static files via app.Static('/') will likely fail or use default.")
	}
	if cfg.CacheDir != "" {
		abs, err := filepath.Abs(cfg.CacheDir)
		if err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("CacheDir '%s' invalid path: %v", cfg.CacheDir, err))
		} else {
			cfg.CacheDir = abs
		}
		if err := ensureDirExists(cfg.CacheDir, logger); err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("CacheDir creation failed: %v", err))
		}
		if err := checkDirWritable(cfg.CacheDir, logger); err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("CacheDir '%s' not writable: %v", cfg.CacheDir, err))
		}
	}
	if cfg.SourcesDir != "" {
		if err := ensureDirExists(cfg.SourcesDir, logger); err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("SourcesDir check/creation failed: %v", err))
		}
	}
	if cfg.StaticsDir != "" {
		if err := ensureDirExists(cfg.StaticsDir, logger); err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("StaticsDir check/creation failed: %v", err))
		}
	}

	cfg.Port = getEnvOrDefault(logger, "PORT", cfg.Port, "8080")
	cfg.ReadTimeout = defaultDuration(cfg.ReadTimeout, 30*time.Second)
	cfg.WriteTimeout = defaultDuration(cfg.WriteTimeout, 30*time.Second)
	cfg.IdleTimeout = defaultDuration(cfg.IdleTimeout, 60*time.Second)
	cfg.WarmupConcurrency = defaultInt(cfg.WarmupConcurrency, 4)
	cfg.CacheL1DefaultTTL = parseDurationEnv("CACHE_L1_DEFAULT_TTL", cfg.CacheL1DefaultTTL, 5*time.Minute, logger)
	cfg.CacheL2DefaultTTL = parseDurationEnv("CACHE_L2_DEFAULT_TTL", cfg.CacheL2DefaultTTL, 24*time.Hour, logger)
	cfg.BadgerGCInterval = parseDurationEnv("BADGER_GC_INTERVAL", cfg.BadgerGCInterval, 1*time.Hour, logger)
	if cfg.BadgerGCDiscardRatio <= 0 || cfg.BadgerGCDiscardRatio >= 1.0 {
		if cfg.BadgerGCDiscardRatio != 0 {
			logger.Warn("Invalid BadgerGCDiscardRatio, using 0.5", "value", cfg.BadgerGCDiscardRatio)
		}
		cfg.BadgerGCDiscardRatio = 0.5
	}

	logger.Info("Effective blitzkit Core Config",
		slog.String("AppName", cfg.AppName), // AJOUTÉ: Log de AppName
		slog.String("Port", cfg.Port), slog.Duration("ReadTimeout", cfg.ReadTimeout), slog.Duration("WriteTimeout", cfg.WriteTimeout),
		slog.Duration("IdleTimeout", cfg.IdleTimeout), slog.Bool("DevMode", cfg.DevMode),
		slog.Bool("MinifyCSS", cfg.MinifyCSS), slog.Bool("MinifyJS", cfg.MinifyJS), slog.Bool("UseEsbuildIfAvailable", cfg.UseEsbuildIfAvailable),
		slog.String("PublicDir", cfg.PublicDir), slog.String("CacheDir", cfg.CacheDir), slog.String("SourcesDir", cfg.SourcesDir), slog.String("StaticsDir", cfg.StaticsDir),
		slog.Duration("CacheL1TTL", cfg.CacheL1DefaultTTL), slog.Duration("CacheL2TTL", cfg.CacheL2DefaultTTL),
		slog.Duration("BadgerGCInterval", cfg.BadgerGCInterval), slog.Float64("BadgerGCDiscardRatio", cfg.BadgerGCDiscardRatio),
		slog.Bool("EnableMetrics", cfg.EnableMetrics), slog.Int("WarmupConcurrency", cfg.WarmupConcurrency),
	)

	var cacheSystem *Cache
	var cacheErr error
	if cfg.CacheDir != "" {
		cacheL1CleanupInterval := parseDurationEnv("CACHE_L1_CLEANUP_INTERVAL", 0, 10*time.Minute, logger)
		cacheSystem, cacheErr = NewCache(cfg.CacheDir, logger, cacheL1CleanupInterval, cfg.BadgerGCInterval, cfg.BadgerGCDiscardRatio)
		if cacheErr != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("Cache init failed: %v", cacheErr))
		}
	} else {
		logger.Warn("CacheDir not configured. L2 cache disabled. Using only L1 cache.")
		cacheL1CleanupInterval := parseDurationEnv("CACHE_L1_CLEANUP_INTERVAL", 0, 10*time.Minute, logger)
		l1 := cache.New(cache.NoExpiration, cacheL1CleanupInterval)
		cacheSystem = &Cache{L1: l1, L2: nil}
	}
	if cacheSystem == nil && cacheErr == nil {
		validationErrors = append(validationErrors, "Cache system initialization returned nil unexpectedly")
	}

	if len(validationErrors) > 0 {
		err := fmt.Errorf("blitzkit config validation failed: %s", strings.Join(validationErrors, "; "))
		logger.Error("Configuration validation failed", "errors", validationErrors)
		if cacheSystem != nil {
			_ = cacheSystem.Close(logger)
		}
		return nil, err
	}
	logger.Debug("Configuration validation passed.")

	s := &Server{
		config:         cfg,
		logger:         logger,
		Cache:          cacheSystem,
		warmupRegistry: make([]WarmupRegistration, 0),
	}

	errorHandler := s.handleError
	if cfg.ErrorHandler != nil {
		errorHandler = cfg.ErrorHandler
	}
	fiberConfig := fiber.Config{
		ReadTimeout:           cfg.ReadTimeout,
		WriteTimeout:          cfg.WriteTimeout,
		IdleTimeout:           cfg.IdleTimeout,
		ErrorHandler:          errorHandler,
		DisableStartupMessage: true,
		AppName:               cfg.AppName, // CORRIGÉ: Utilise cfg.AppName initialisé
		Prefork:               !cfg.DevMode,
	}
	app := fiber.New(fiberConfig)
	s.app = app

	if cfg.PublicDir != "" && (cfg.SourcesDir != "" || cfg.StaticsDir != "") {
		processor := NewStaticProcessor(cfg.SourcesDir, cfg.StaticsDir, cfg.PublicDir, logger,
			cfg.DevMode, cfg.MinifyCSS, cfg.MinifyJS, cfg.UseEsbuildIfAvailable)
		if err := processor.Process(); err != nil {
			logger.Error("Failed to process static files on startup, check permissions and paths", "error", err)
		}
	} else {
		logger.Info("Static file processing skipped (required directories not configured or PublicDir invalid).")
	}

	s.setupBaseMiddlewares()

	var l2db *badger.DB = nil
	if s.Cache != nil {
		l2db = s.Cache.L2
	}
	setupMonitoring(app, cfg, logger, l2db)

	logger.Info("blitzkit instance created. Register warmup items via handlers/init.")
	return s, nil
}

// ... (le reste de server.go reste inchangé : App, GetLogger, GetConfig, Start, Shutdown, RegisterForWarmup, ExecuteWarmup) ...
// App returns the underlying Fiber application instance.
// It panics if the Fiber instance has not been initialized.
func (s *Server) App() *fiber.App {
	if s.app == nil {
		panic("Server.App() called on a nil Fiber instance")
	}
	return s.app
}

// GetLogger returns the slog.Logger instance configured for this server.
func (s *Server) GetLogger() *slog.Logger {
	return s.logger
}

// GetConfig returns a copy of the server's effective configuration.
func (s *Server) GetConfig() Config {
	return s.config
}

// Start starts the Fiber web server, listening on the configured port.
// It blocks until the server is shut down (either by Shutdown or a fatal error).
// Startup messages are logged based on development/production mode and master/child process status.
func (s *Server) Start() error {
	port := s.config.Port
	if port == "" {
		port = "8080"
	}
	addr := fmt.Sprintf(":%s", port)
	preforkStatus := "disabled"
	if !s.config.DevMode && fiber.IsChild() {
		preforkStatus = "enabled (child)"
	} else if !s.config.DevMode {
		preforkStatus = "enabled (master)"
	}

	if !s.config.DevMode && fiber.IsChild() {
		// Child processes in prefork mode should not log the main startup message.
	} else {
		s.logger.Info("Server starting listener...", "address", addr, "dev_mode", s.config.DevMode, "prefork", preforkStatus)
	}

	if err := s.app.Listen(addr); err != nil {
		if !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("Server failed to start or stopped unexpectedly", "address", addr, "error", err)
			return fmt.Errorf("server listen failed on %s: %w", addr, err)
		}
		if !(!s.config.DevMode && fiber.IsChild()) {
			s.logger.Info("Server stopped listening gracefully", "address", addr)
		}
	}
	return nil
}

// Shutdown attempts to gracefully shut down the web server and close the cache system.
// It uses Fiber's ShutdownWithTimeout and calls Cache.Close().
// It logs the process and returns the first error encountered.
// Child processes in prefork mode do not execute the full shutdown.
func (s *Server) Shutdown() error {
	if !s.config.DevMode && fiber.IsChild() {
		s.logger.Debug("Prefork child process shutting down", "pid", os.Getpid())
		return nil
	}

	s.logger.Info("Initiating graceful shutdown...")
	shutdownTimeout := 30 * time.Second
	var wg sync.WaitGroup
	var firstErr error
	errChan := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.logger.Debug("Shutting down Fiber application...")
		if s.app != nil {
			if err := s.app.ShutdownWithTimeout(shutdownTimeout); err != nil {
				if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, http.ErrServerClosed) {
					s.logger.Error("Fiber shutdown error", "error", err)
					errChan <- fmt.Errorf("fiber shutdown failed: %w", err)
				} else if errors.Is(err, context.DeadlineExceeded) {
					s.logger.Warn("Fiber shutdown timed out waiting for connections to close", "timeout", shutdownTimeout)
				} else {
					s.logger.Debug("Fiber shutdown completed (ErrServerClosed received).")
				}
			} else {
				s.logger.Debug("Fiber shutdown completed successfully.")
			}
		} else {
			s.logger.Warn("Fiber app was nil during shutdown.")
		}
	}()

	if s.Cache != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.logger.Debug("Closing cache system...")
			if err := s.Cache.Close(s.logger); err != nil {
				s.logger.Error("Cache system close error", "error", err)
				errChan <- fmt.Errorf("cache system close failed: %w", err)
			} else {
				s.logger.Debug("Cache system closed successfully.")
			}
		}()
	} else {
		s.logger.Debug("Cache system is nil, skipping close.")
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if firstErr == nil {
			firstErr = err
		} else {
			s.logger.Error("Additional shutdown error occurred", "subsequent_error", err)
		}
	}

	if firstErr == nil {
		s.logger.Info("Server shutdown procedures completed successfully.")
	} else {
		s.logger.Error("Server shutdown procedures completed with errors.", "first_error", firstErr)
	}
	return firstErr
}

// RegisterForPageWarmup registers a PageGeneratorFunc for the cache warmup process.
// The generator should return a templ.Component and a lastModified Unix timestamp.
func (s *Server) RegisterForPageWarmup(key string, ttlInfo CacheTTLInfo, generator PageGeneratorFunc) {
	s.warmupMutex.Lock()
	defer s.warmupMutex.Unlock()
	if generator == nil {
		s.logger.Warn("Skipping page warmup registration: generator is nil", "key", key)
		return
	}
	s.logger.Debug("Registering page for warmup", "key", key)
	s.warmupRegistry = append(s.warmupRegistry, WarmupRegistration{
		Key:           key,
		GeneratorFunc: generator,
		IsBytes:       false,
		TTLInfo:       ttlInfo,
	})
}

// RegisterForBytesWarmup registers a BytesGeneratorFunc for the cache warmup process.
// The generator should return a byte slice and a lastModified Unix timestamp.
func (s *Server) RegisterForBytesWarmup(key string, ttlInfo CacheTTLInfo, generator BytesGeneratorFunc) {
	s.warmupMutex.Lock()
	defer s.warmupMutex.Unlock()
	if generator == nil {
		s.logger.Warn("Skipping bytes warmup registration: generator is nil", "key", key)
		return
	}
	s.logger.Debug("Registering bytes for warmup", "key", key)
	s.warmupRegistry = append(s.warmupRegistry, WarmupRegistration{
		Key:           key,
		GeneratorFunc: generator,
		IsBytes:       true,
		TTLInfo:       ttlInfo,
	})
}

// ExecuteWarmup runs the cache warmup process for all registered items.
// It generates content for each item (if not already in L1 cache) and stores it in L1 and L2 caches.
// Concurrency is controlled by Config.WarmupConcurrency.
// It returns an aggregated error if any items fail.
func (s *Server) ExecuteWarmup() error {
	s.warmupMutex.Lock()
	itemsToWarmup := make([]WarmupRegistration, len(s.warmupRegistry))
	copy(itemsToWarmup, s.warmupRegistry)
	s.warmupMutex.Unlock()

	if len(itemsToWarmup) == 0 {
		s.logger.Info("No items registered for cache warmup.")
		return nil
	}
	if s.Cache == nil {
		s.logger.Error("Cannot perform warmup: Cache system is not initialized.")
		return fmt.Errorf("cache system not initialized, cannot perform warmup")
	}

	maxConcurrent := s.config.WarmupConcurrency
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	s.logger.Info("Starting registered cache warm-up...", "count", len(itemsToWarmup), "concurrency", maxConcurrent)

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrent)
	errChan := make(chan error, len(itemsToWarmup))
	start := time.Now()

	for i, itemReg := range itemsToWarmup {
		currentIndex := i
		currentItem := itemReg

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			logCtx := s.logger.With("cache_key", currentItem.Key, "item_index", currentIndex+1)
			itemStart := time.Now()

			if s.Cache.L1 != nil {
				if _, found := s.Cache.L1.Get(currentItem.Key); found {
					logCtx.Debug("[Warmup] Skipped: Already in L1")
					IncCacheWarmupSkipped()
					ObserveCacheWarmupDuration(time.Since(itemStart).Seconds())
					return
				}
			}

			var generatedBytes []byte
			var lastModified int64 = time.Now().Unix()
			var genErr error

			logCtx.Debug("[Warmup] Generating content")
			if currentItem.IsBytes {
				if generator, ok := currentItem.GeneratorFunc.(BytesGeneratorFunc); ok && generator != nil {
					generatedBytes, lastModified, genErr = generator()
				} else {
					genErr = fmt.Errorf("invalid or nil generator type for bytes warmup")
				}
			} else {
				if generator, ok := currentItem.GeneratorFunc.(PageGeneratorFunc); ok && generator != nil {
					var component templ.Component
					component, lastModified, genErr = generator()
					if genErr == nil && component != nil {
						var buf bytes.Buffer
						renderErr := component.Render(context.Background(), &buf)
						if renderErr != nil {
							genErr = fmt.Errorf("render failed: %w", renderErr)
						} else {
							generatedBytes = buf.Bytes()
						}
					} else if genErr == nil {
						genErr = errors.New("generator returned nil component")
					}
				} else {
					genErr = fmt.Errorf("invalid or nil generator type for page warmup")
				}
			}

			if genErr != nil {
				logCtx.Error("[Warmup] Generator/Render failed", "error", genErr)
				errChan <- fmt.Errorf("key %s: %w", currentItem.Key, genErr)
				IncCacheWarmupErrors()
				return
			}
			if generatedBytes == nil {
				logCtx.Error("[Warmup] Generator produced nil bytes")
				errChan <- fmt.Errorf("key %s: generator produced nil bytes", currentItem.Key)
				IncCacheWarmupErrors()
				return
			}

			l2StoreErr := s.storeInL2(currentItem.Key, CacheEntry{Data: generatedBytes, LastModified: lastModified}, currentItem.TTLInfo.IsInfinite)
			if l2StoreErr != nil {
				logCtx.Warn("[Warmup] Failed to store in L2, proceeding with L1 if available", "error", l2StoreErr)
			} else {
				logCtx.Debug("[Warmup] Stored in L2 successfully")
			}

			if s.Cache.L1 != nil {
				l1TTL := s.config.CacheL1DefaultTTL
				if currentItem.TTLInfo.IsInfinite {
					l1TTL = cache.NoExpiration
				}
				s.Cache.L1.Set(currentItem.Key, generatedBytes, l1TTL)
				IncCacheL1Set()
				logCtx.Debug("[Warmup] Stored in L1 successfully", "l1_ttl", l1TTL)
			}

			logCtx.Debug("[Warmup] Item processed successfully", "duration", time.Since(itemStart))
			ObserveCacheWarmupDuration(time.Since(itemStart).Seconds())
		}()
	}

	wg.Wait()
	close(errChan)
	totalDuration := time.Since(start)
	ObserveCacheWarmupTotalDuration(totalDuration.Seconds())

	var warmupErrs []string
	errorCount := 0
	for err := range errChan {
		warmupErrs = append(warmupErrs, err.Error())
		errorCount++
	}
	if errorCount > 0 {
		fullError := fmt.Errorf("%d errors during cache warm-up: %s", errorCount, strings.Join(warmupErrs, "; "))
		s.logger.Error("Registered cache warm-up completed with errors", "details", fullError.Error())
		return fullError
	}
	s.logger.Info("Registered cache warm-up finished.", "duration", totalDuration)
	return nil
}
