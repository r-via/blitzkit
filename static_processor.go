// File: pkg/blitzkitgo/static_processor.go
// Description: Gère le traitement des fichiers statiques (CSS, JS) au démarrage du serveur.
//
//	Inclut la minification des fichiers sources et la copie des fichiers statiques
//	vers le répertoire public.
package blitzkitgo

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/js"
)

// StaticProcessor gère la minification et la copie des ressources statiques.
// Il lit depuis `sourcesDir` (pour minifier CSS/JS) et `staticsDir` (pour copier),
// et écrit le résultat dans `publicDir`.
type StaticProcessor struct {
	sourcesDir string
	staticsDir string
	publicDir  string
	logger     *slog.Logger
	minifier   *minify.M
	devMode    bool
}

// NewStaticProcessor crée une nouvelle instance de StaticProcessor.
// Initialise le minificateur pour CSS et JS.
//
// Args:
//
//	sourcesDir (string): Répertoire contenant les fichiers CSS/JS à minifier.
//	staticsDir (string): Répertoire contenant les fichiers statiques à copier tels quels.
//	publicDir (string): Répertoire de destination où les fichiers traités seront écrits.
//	logger (*slog.Logger): Le logger structuré.
//	devMode (bool): Indicateur du mode développement (peut affecter la minification ou la sélection de fichiers).
//
// Returns:
//
//	*StaticProcessor: Une nouvelle instance de StaticProcessor.
func NewStaticProcessor(sourcesDir, staticsDir, publicDir string, logger *slog.Logger, devMode bool) *StaticProcessor {
	if logger == nil {
		logger = slog.Default()
		logger.Warn("StaticProcessor using default logger.")
	}
	m := minify.New()
	m.AddFunc("text/css", css.Minify)
	m.AddFunc("application/javascript", js.Minify)

	return &StaticProcessor{
		sourcesDir: sourcesDir,
		staticsDir: staticsDir,
		publicDir:  publicDir,
		logger:     logger,
		minifier:   m,
		devMode:    devMode,
	}
}

// Process exécute le pipeline complet de traitement des ressources statiques :
// 1. Purge (supprime et recrée) le répertoire public de destination.
// 2. Minifie les fichiers CSS et JS trouvés dans le répertoire source.
// 3. Copie tous les fichiers et répertoires du répertoire statique.
// Retourne une erreur agrégée si des étapes échouent.
//
// Returns:
//
//	error: Une erreur si la purge, la minification ou la copie échoue, sinon nil.
func (sp *StaticProcessor) Process() error {
	logCtx := sp.logger.With(
		slog.String("sources_dir", sp.sourcesDir),
		slog.String("statics_dir", sp.staticsDir),
		slog.String("public_dir", sp.publicDir),
		slog.Bool("dev_mode", sp.devMode),
	)
	logCtx.Info("Processing static assets...")

	if sp.publicDir != "" {
		if err := sp.purgePublicDir(); err != nil {
			logCtx.Error("Failed to purge public directory, processing might overwrite files", "error", err)
		}
	} else {
		logCtx.Error("PublicDir is not configured, cannot process static assets.")
		return errors.New("PublicDir is required for StaticProcessor")
	}

	var processErrors []string

	if sp.sourcesDir != "" {
		if err := sp.minifyFiles(); err != nil {
			logCtx.Error("Error during source file minification", "error", err)
			processErrors = append(processErrors, fmt.Sprintf("minify failed: %v", err))
		}
	} else {
		logCtx.Info("SourcesDir not configured, skipping minification.")
	}

	if sp.staticsDir != "" {
		if err := sp.copyFiles(); err != nil {
			logCtx.Error("Error during static file copying", "error", err)
			processErrors = append(processErrors, fmt.Sprintf("copy failed: %v", err))
		}
	} else {
		logCtx.Info("StaticsDir not configured, skipping static file copy.")
	}

	if len(processErrors) > 0 {
		logCtx.Warn("Static asset processing completed with errors.", "errors", processErrors)
		return fmt.Errorf("static processing errors: %s", strings.Join(processErrors, "; "))
	}

	logCtx.Info("Static asset processing completed successfully.")
	return nil
}

// purgePublicDir supprime le contenu existant du répertoire public et le recrée.
// Contient des sécurités pour éviter de supprimer des chemins invalides (ex: "/", ".").
//
// Returns:
//
//	error: Une erreur si le chemin est invalide, si la suppression ou la recréation échoue.
func (sp *StaticProcessor) purgePublicDir() error {
	logCtx := sp.logger.With(slog.String("public_dir", sp.publicDir))
	logCtx.Debug("Purging public directory...")

	if sp.publicDir == "" || sp.publicDir == "/" || sp.publicDir == "." {
		logCtx.Error("Refusing to purge invalid public directory path")
		return errors.New("invalid public directory path for purging")
	}

	if _, err := os.Stat(sp.publicDir); err == nil {
		errRemove := os.RemoveAll(sp.publicDir)
		if errRemove != nil {
			logCtx.Error("Failed to remove existing public directory", "error", errRemove)
			return fmt.Errorf("failed to remove %s: %w", sp.publicDir, errRemove)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		logCtx.Error("Failed to stat public directory before purge", "error", err)
		return fmt.Errorf("failed to stat %s before purge: %w", sp.publicDir, err)
	}

	if err := os.MkdirAll(sp.publicDir, 0755); err != nil {
		logCtx.Error("Failed to recreate public directory", "error", err)
		return fmt.Errorf("failed to create %s: %w", sp.publicDir, err)
	}

	logCtx.Debug("Public directory purged and recreated.")
	return nil
}

// minifyFiles parcourt le répertoire `sourcesDir`, identifie les fichiers CSS et JS
// (en gérant la priorité des fichiers `.debug.js` en mode dev), les minifie
// (sauf si désactivé pour JS en mode dev), et écrit le résultat dans le `publicDir`
// en conservant la structure de sous-répertoires relative.
//
// Returns:
//
//	error: Une erreur si le parcours du répertoire ou une opération de fichier/minification échoue.
func (sp *StaticProcessor) minifyFiles() error {
	logCtx := sp.logger.With(slog.String("sources_dir", sp.sourcesDir), slog.String("public_dir", sp.publicDir))
	logCtx.Info("Minifying source assets (CSS/JS)...")

	dirInfo, err := os.Stat(sp.sourcesDir)
	if os.IsNotExist(err) {
		logCtx.Info("Source directory does not exist, skipping minification.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed stat %s: %w", sp.sourcesDir, err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("%s is not a directory", sp.sourcesDir)
	}

	filesProcessed := 0
	filesToProcess := make(map[string]string)

	err = filepath.WalkDir(sp.sourcesDir, func(srcPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			logCtx.Error("Walk error", "path", srcPath, "error", walkErr)
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		fileName := d.Name()
		isCSS := strings.HasSuffix(fileName, ".css")
		isDebugJS := strings.HasSuffix(fileName, ".debug.js")
		isStandardJS := strings.HasSuffix(fileName, ".js") && !isDebugJS

		if !isCSS && !isDebugJS && !isStandardJS {
			return nil
		}

		baseFileName := fileName
		if isDebugJS {
			baseFileName = strings.TrimSuffix(fileName, ".debug.js") + ".js"
		}

		relSrcPath, errRel := filepath.Rel(sp.sourcesDir, srcPath)
		if errRel != nil {
			logCtx.Error("Failed get relative path", "file", srcPath, "error", errRel)
			return nil
		}

		destRelPath := filepath.ToSlash(filepath.Join(filepath.Dir(relSrcPath), baseFileName))
		destPath := filepath.Join(sp.publicDir, destRelPath)

		currentSrc, exists := filesToProcess[destPath]
		shouldReplace := !exists
		if exists {
			currentIsDebug := strings.HasSuffix(currentSrc, ".debug.js")
			newIsDebug := isDebugJS
			if sp.devMode && newIsDebug && !currentIsDebug {
				shouldReplace = true
			}
			if !sp.devMode && !newIsDebug && currentIsDebug {
				shouldReplace = true
			}
		}

		if shouldReplace {
			if sp.devMode && exists {
				logCtx.Debug("Replacing file candidate for DevMode", "destination", destPath, "old_source", currentSrc, "new_source", srcPath)
			}
			if !sp.devMode && exists {
				logCtx.Debug("Replacing file candidate for ProdMode", "destination", destPath, "old_source", currentSrc, "new_source", srcPath)
			}
			filesToProcess[destPath] = srcPath
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed scanning %s for minification: %w", sp.sourcesDir, err)
	}

	logCtx.Debug("Processing selected files for minification", "count", len(filesToProcess))
	for destPath, srcPath := range filesToProcess {
		fileLogCtx := logCtx.With(slog.String("source", srcPath), slog.String("destination", destPath))
		if sp.devMode {
			fileLogCtx.Debug("Processing file")
		}

		destSubDir := filepath.Dir(destPath)
		if err := os.MkdirAll(destSubDir, 0755); err != nil {
			fileLogCtx.Error("Failed create destination subdir", "path", destSubDir, "error", err)
			continue
		}

		srcContent, errRead := os.ReadFile(srcPath)
		if errRead != nil {
			fileLogCtx.Error("Failed read source file", "error", errRead)
			continue
		}

		var mediaType string
		if strings.HasSuffix(srcPath, ".css") {
			mediaType = "text/css"
		} else {
			mediaType = "application/javascript"
		}

		minifiedContent, minifyErr := sp.minifier.Bytes(mediaType, srcContent)
		if minifyErr != nil {
			fileLogCtx.Error("Minification failed, using original content", "error", minifyErr)
			minifiedContent = srcContent
		}

		if errWrite := os.WriteFile(destPath, minifiedContent, 0644); errWrite != nil {
			fileLogCtx.Error("Failed write destination file", "error", errWrite)
			continue
		}
		filesProcessed++
	}

	logCtx.Info("Source asset minification completed", "files_processed", filesProcessed)
	return nil
}

// copyFiles parcourt le répertoire `staticsDir` et copie récursivement tous
// les fichiers et sous-répertoires vers le `publicDir`, en préservant la structure.
//
// Returns:
//
//	error: Une erreur si le parcours ou une opération de copie/création de répertoire échoue.
func (sp *StaticProcessor) copyFiles() error {
	logCtx := sp.logger.With(slog.String("statics_dir", sp.staticsDir), slog.String("public_dir", sp.publicDir))
	logCtx.Info("Copying static assets...")

	dirInfo, err := os.Stat(sp.staticsDir)
	if os.IsNotExist(err) {
		logCtx.Info("Statics directory does not exist, skipping copy.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed stat %s: %w", sp.staticsDir, err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("%s is not a directory", sp.staticsDir)
	}

	filesCopiedCount := 0
	dirsCreatedCount := 0

	err = filepath.WalkDir(sp.staticsDir, func(srcPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			logCtx.Error("Walk error during copy", "path", srcPath, "error", walkErr)
			return walkErr
		}
		if srcPath == sp.staticsDir {
			return nil
		}

		relPath, errRel := filepath.Rel(sp.staticsDir, srcPath)
		if errRel != nil {
			logCtx.Error("Failed get relative path during copy", "path", srcPath, "error", errRel)
			return nil
		}

		destPath := filepath.Join(sp.publicDir, relPath)

		if d.IsDir() {
			if errMkdir := os.MkdirAll(destPath, 0755); errMkdir != nil {
				logCtx.Error("Failed create destination directory during copy", "path", destPath, "error", errMkdir)
				return errMkdir
			}
			dirsCreatedCount++
			if sp.devMode {
				logCtx.Debug("Created directory", "destination", destPath)
			}
			return nil
		} else {
			errCopy := copyFile(srcPath, destPath)
			if errCopy != nil {
				logCtx.Error("Failed to copy file", "source", srcPath, "destination", destPath, "error", errCopy)
				return nil
			}
			filesCopiedCount++
			if sp.devMode {
				logCtx.Debug("Copied file", "source", srcPath, "destination", destPath)
			}
			return nil
		}
	})

	if err != nil {
		return fmt.Errorf("failed during walk for copy from %s: %w", sp.staticsDir, err)
	}
	logCtx.Info("Static file copy process completed", "files_copied", filesCopiedCount, "directories_created", dirsCreatedCount)
	return nil
}

// copyFile copie le contenu d'un fichier source vers un fichier destination.
// Crée ou écrase le fichier destination.
//
// Args:
//
//	src (string): Chemin du fichier source.
//	dst (string): Chemin du fichier destination.
//
// Returns:
//
//	error: Une erreur si l'ouverture, la création ou la copie échoue.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %s: %w", src, err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination %s: %w", dst, err)
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}

	return nil
}
