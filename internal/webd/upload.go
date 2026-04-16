package webd

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxUploadSize = 20 * 1024 * 1024 // 20 MB
	uploadSubDir  = "web-uploads"
)

// UploadResult is returned to the browser after a successful upload.
type UploadResult struct {
	Path string `json:"path"` // absolute path inside the container (accessible to MCP tools)
	Name string `json:"name"` // original filename
	Size int64  `json:"size"` // bytes
}

// handleUpload handles POST /api/upload (multipart/form-data, field "file").
// Saves the file to the configured upload directory and returns metadata the
// client embeds as a reference in the message content.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+1024)
	if err := r.ParseMultipartForm(4 * 1024 * 1024); err != nil {
		http.Error(w, "file too large or invalid form", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if header.Size > maxUploadSize {
		http.Error(w, fmt.Sprintf("file exceeds %d MB limit", maxUploadSize/1024/1024), http.StatusRequestEntityTooLarge)
		return
	}

	uploadDir := s.uploadDir()
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Prefix filename with a random token to avoid collisions and path traversal.
	safeName := sanitizeFilename(header.Filename)
	token, err := randomPrefix()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	destName := token + "_" + safeName
	destPath := filepath.Join(uploadDir, destName)

	dst, err := os.Create(destPath)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		os.Remove(destPath)
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(UploadResult{
		Path: destPath,
		Name: header.Filename,
		Size: written,
	})
}

// uploadDir returns the directory where uploaded files are saved.
func (s *Server) uploadDir() string {
	base := envOr("WEBD_UPLOAD_DIR", "/tmp/mcp-workspace/"+uploadSubDir)
	return base
}

func sanitizeFilename(name string) string {
	// Keep only the base name, replace spaces, strip dangerous chars.
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, " ", "_")
	var safe strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '.' || r == '-' || r == '_' {
			safe.WriteRune(r)
		}
	}
	result := safe.String()
	if result == "" {
		return "upload"
	}
	return result
}

func randomPrefix() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
