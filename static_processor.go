// Package blitzkit provides utility functions and core server components,
// including static file processing capabilities.
package blitzkit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/js"
)

// StaticProcessor handles minification and copying of static assets.
// It reads from sourcesDir (for CSS/JS to minify) and staticsDir (to copy as-is),
// and writes processed files to publicDir.
// Minification behavior is controlled by MinifyCSS, MinifyJS, and UseEsbuildIfAvailable settings.
type StaticProcessor struct {
	sourcesDir            string
	staticsDir            string
	publicDir             string
	logger                *slog.Logger
	minifier              *minify.M // Internal Go-based minifier (tdewolff)
	devMode               bool
	minifyCSS             bool // From Config: whether to minify CSS
	minifyJS              bool // From Config: whether to minify JS
	useEsbuildIfAvailable bool // From Config: whether to attempt using esbuild for JS
	esbuildPath           string
}

// NewStaticProcessor creates a new instance of StaticProcessor.
// It initializes the internal minifier for CSS and JS (as a fallback)
// and detects the presence of esbuild if configured for use.
// Minification behavior is determined by the passed boolean flags.
func NewStaticProcessor(
	sourcesDir, staticsDir, publicDir string,
	logger *slog.Logger,
	devMode bool,
	confMinifyCSS bool,
	confMinifyJS bool,
	confUseEsbuild bool,
) *StaticProcessor {
	if logger == nil {
		logger = slog.Default()
		logger.Warn("StaticProcessor using default logger.")
	}
	m := minify.New()
	m.AddFunc("text/css", css.Minify)
	m.AddFunc("application/javascript", js.Minify)

	sp := &StaticProcessor{
		sourcesDir:            sourcesDir,
		staticsDir:            staticsDir,
		publicDir:             publicDir,
		logger:                logger,
		minifier:              m,
		devMode:               devMode,
		minifyCSS:             confMinifyCSS,
		minifyJS:              confMinifyJS,
		useEsbuildIfAvailable: confUseEsbuild,
	}

	if sp.useEsbuildIfAvailable && sp.minifyJS {
		path, err := exec.LookPath("esbuild")
		if err == nil && path != "" {
			sp.esbuildPath = path
			logger.Info("esbuild detected and configured for use with JS minification.", "path", path)
		} else {
			logger.Info("esbuild not found in PATH or not configured for use. Internal Go-based minifier will be used for JavaScript if minification is enabled.")
			sp.useEsbuildIfAvailable = false // Ensure it's false if not found or not configured
		}
	} else if sp.minifyJS {
		logger.Info("Internal Go-based minifier will be used for JavaScript if minification is enabled (esbuild usage not configured or JS minification disabled).")
	} else {
		logger.Info("JavaScript minification is disabled via configuration.")
	}
	if !sp.minifyCSS {
		logger.Info("CSS minification is disabled via configuration.")
	}

	return sp
}

// Process executes the static asset processing pipeline:
// 1. Purges (deletes and recreates) the public destination directory.
// 2. Minifies CSS and JS files found in the sources directory, based on configuration.
// 3. Copies all files and directories from the statics directory.
// It returns an aggregated error if any steps fail.
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
			logCtx.Error("Error during source file processing (minification/copy)", "error", err)
			processErrors = append(processErrors, fmt.Sprintf("source processing failed: %v", err))
		}
	} else {
		logCtx.Info("SourcesDir not configured, skipping source file processing.")
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

// purgePublicDir deletes the existing content of the public directory and recreates it.
// It includes safeguards to prevent deleting invalid paths (e.g., "/", ".").
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

// minifyFiles walks the sourcesDir, processes .css and .js files based on
// the MinifyCSS, MinifyJS, and UseEsbuildIfAvailable configuration.
// If minification is disabled for a type, files are copied as-is.
// Esbuild is preferred for JS minification if enabled and available; otherwise,
// an internal Go minifier is used as a fallback or primary if esbuild is not used.
// Output files maintain their relative subdirectory structure within publicDir.
func (sp *StaticProcessor) minifyFiles() error {
	logCtx := sp.logger.With(
		slog.String("sources_dir", sp.sourcesDir),
		slog.String("public_dir", sp.publicDir),
		slog.Bool("minify_css", sp.minifyCSS),
		slog.Bool("minify_js", sp.minifyJS),
		slog.Bool("use_esbuild", sp.useEsbuildIfAvailable && sp.esbuildPath != ""),
	)
	logCtx.Info("Processing source assets from SourcesDir...")

	dirInfo, err := os.Stat(sp.sourcesDir)
	if os.IsNotExist(err) {
		logCtx.Info("Source directory does not exist, skipping processing.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stat sources directory %s: %w", sp.sourcesDir, err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("sources path %s is not a directory", sp.sourcesDir)
	}

	filesProcessed := 0
	err = filepath.WalkDir(sp.sourcesDir, func(srcPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			logCtx.Error("Error during directory walk", "path", srcPath, "error", walkErr)
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		fileName := d.Name()
		isCSS := strings.HasSuffix(fileName, ".css")
		isJS := strings.HasSuffix(fileName, ".js")

		if !isCSS && !isJS {
			return nil // Process only .css and .js files
		}

		relSrcPath, errRel := filepath.Rel(sp.sourcesDir, srcPath)
		if errRel != nil {
			logCtx.Error("Failed to get relative path", "source_file", srcPath, "error", errRel)
			return nil
		}
		destRelPath := filepath.ToSlash(relSrcPath)
		destPath := filepath.Join(sp.publicDir, destRelPath)
		fileLogCtx := logCtx.With(slog.String("source", srcPath), slog.String("destination", destPath))

		destSubDir := filepath.Dir(destPath)
		if err := os.MkdirAll(destSubDir, 0755); err != nil {
			fileLogCtx.Error("Failed to create destination subdirectory", "path", destSubDir, "error", err)
			return nil
		}

		srcContent, errRead := os.ReadFile(srcPath)
		if errRead != nil {
			fileLogCtx.Error("Failed to read source file", "error", errRead)
			return nil
		}

		outputContent := srcContent // Default to original content (copy)
		var opErr error
		esbuildProcessedFile := false

		if isCSS {
			if sp.minifyCSS {
				fileLogCtx.Debug("Minifying CSS with internal Go minifier...")
				outputContent, opErr = sp.minifier.Bytes("text/css", srcContent)
				if opErr != nil {
					fileLogCtx.Error("CSS minification failed, using original content", "error", opErr)
					outputContent = srcContent
				}
			} else {
				fileLogCtx.Debug("CSS minification disabled, copying as is.")
			}
		} else if isJS {
			if sp.minifyJS {
				if sp.useEsbuildIfAvailable && sp.esbuildPath != "" {
					fileLogCtx.Debug("Attempting JS minification with esbuild...")
					cmd := exec.Command(sp.esbuildPath, srcPath, "--minify", "--outfile="+destPath)
					var stderrBuf bytes.Buffer
					cmd.Stderr = &stderrBuf
					if errCmd := cmd.Run(); errCmd != nil {
						opErr = fmt.Errorf("esbuild failed: %v, stderr: %s", errCmd, stderrBuf.String())
						fileLogCtx.Error("esbuild minification failed, attempting fallback to internal Go minifier", "error", opErr)

						outputContent, opErr = sp.minifier.Bytes("application/javascript", srcContent)
						if opErr != nil {
							fileLogCtx.Error("Fallback JS (internal Go) minification failed, using original content", "error", opErr)
							outputContent = srcContent
						}
					} else {
						esbuildProcessedFile = true
						fileLogCtx.Debug("JS minified successfully with esbuild and written to output.")
					}
				} else {
					fileLogCtx.Debug("Minifying JS with internal Go minifier (esbuild not used or not available).")
					outputContent, opErr = sp.minifier.Bytes("application/javascript", srcContent)
					if opErr != nil {
						fileLogCtx.Error("Internal JS minification failed, using original content", "error", opErr)
						outputContent = srcContent
					}
				}
			} else {
				fileLogCtx.Debug("JS minification disabled, copying as is.")
			}
		}

		if !esbuildProcessedFile {
			if errWrite := os.WriteFile(destPath, outputContent, 0644); errWrite != nil {
				fileLogCtx.Error("Failed to write destination file", "error", errWrite)
				return nil
			}
		}
		filesProcessed++
		return nil
	})

	if err != nil {
		return fmt.Errorf("error walking sources directory %s: %w", sp.sourcesDir, err)
	}

	logCtx.Info("Source asset processing completed.", "files_processed", filesProcessed)
	return nil
}

// copyFiles walks the staticsDir and recursively copies all files and subdirectories
// to the publicDir, preserving the structure.
func (sp *StaticProcessor) copyFiles() error {
	logCtx := sp.logger.With(slog.String("statics_dir", sp.staticsDir), slog.String("public_dir", sp.publicDir))
	logCtx.Info("Copying static assets from StaticsDir...")

	dirInfo, err := os.Stat(sp.staticsDir)
	if os.IsNotExist(err) {
		logCtx.Info("Statics directory does not exist, skipping copy.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stat statics directory %s: %w", sp.staticsDir, err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("statics path %s is not a directory", sp.staticsDir)
	}

	filesCopiedCount := 0
	dirsCreatedCount := 0

	err = filepath.WalkDir(sp.staticsDir, func(srcPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			logCtx.Error("Error during statics directory walk", "path", srcPath, "error", walkErr)
			return walkErr
		}
		if srcPath == sp.staticsDir {
			return nil
		}

		relPath, errRel := filepath.Rel(sp.staticsDir, srcPath)
		if errRel != nil {
			logCtx.Error("Failed to get relative path during copy", "source_file", srcPath, "error", errRel)
			return nil
		}

		destPath := filepath.Join(sp.publicDir, relPath)

		if d.IsDir() {
			if errMkdir := os.MkdirAll(destPath, 0755); errMkdir != nil {
				logCtx.Error("Failed to create destination directory during copy", "path", destPath, "error", errMkdir)
				return errMkdir
			}
			dirsCreatedCount++
			if sp.devMode {
				logCtx.Debug("Created directory", "destination", destPath)
			}
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
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("error walking statics directory %s for copy: %w", sp.staticsDir, err)
	}
	logCtx.Info("Static file copy process completed.", "files_copied", filesCopiedCount, "directories_created", dirsCreatedCount)
	return nil
}

// copyFile copies the content of a source file to a destination file.
// It creates or overwrites the destination file.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %w", src, err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", dst, err)
	}
	defer dstFile.Close()

	if _, err = io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy from %s to %s: %w", src, dst, err)
	}
	return nil
}
