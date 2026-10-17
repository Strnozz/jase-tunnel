package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type modelFile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Format string `json:"format"`
	Path   string `json:"-"`
	Folder bool   `json:"-"`
}

func defaultModelDirectories() []string {
	home, _ := os.UserHomeDir()
	paths := []string{filepath.Join(home, ".ollama", "models"), filepath.Join(home, ".lmstudio", "models"), filepath.Join(home, ".cache", "lm-studio", "models"), filepath.Join(home, ".cache", "huggingface", "hub")}
	if custom := os.Getenv("OLLAMA_MODELS"); custom != "" && filepath.IsAbs(custom) {
		paths = append(paths, custom)
	}
	return paths
}

// Inspect names/metadata in model directories only, never the whole disk or weights.
// Blob stores and symlink directories are not followed. Paths remain on this computer.
func scanModelFiles(directories []string) []modelFile {
	result := []modelFile{}
	seen := map[string]bool{}
	for _, root := range directories {
		visited := 0
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			visited++
			if visited > 5000 || len(result) >= 100 {
				return fs.SkipAll
			}
			if entry.IsDir() {
				if entry.Name() == "blobs" || entry.Name() == ".git" {
					return fs.SkipDir
				}
				return nil
			}
			format := ""
			folder := false
			name := entry.Name()
			lower := strings.ToLower(name)
			if strings.HasSuffix(lower, ".gguf") && !strings.HasPrefix(lower, "mmproj") && !strings.Contains(lower, "tokenizer") {
				format = "GGUF"
			}
			if name == "config.json" {
				entries, readErr := os.ReadDir(filepath.Dir(path))
				if readErr == nil {
					for _, sibling := range entries {
						if strings.HasSuffix(sibling.Name(), ".safetensors") {
							format = "Safetensors"
							folder = true
							path = filepath.Dir(path)
							name = filepath.Base(path)
							break
						}
					}
				}
			}
			if format == "" || seen[path] {
				return nil
			}
			seen[path] = true
			digest := sha256.Sum256([]byte(path))
			result = append(result, modelFile{ID: hex.EncodeToString(digest[:16]), Name: name, Format: format, Path: path, Folder: folder})
			return nil
		})
	}
	return result
}
