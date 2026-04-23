package webd

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	maxUploadSize = 20 * 1024 * 1024 // 20 MB
	uploadSubDir  = "web-uploads"
)

// UploadResult is returned to the browser after a successful upload.
// URL is a presigned S3 GET URL when S3 is configured; Path is set
// only in local-filesystem fallback mode (dev/single-node).
type UploadResult struct {
	URL  string `json:"url,omitempty"`  // presigned S3 URL (S3 mode)
	Path string `json:"path,omitempty"` // local filesystem path (fallback mode)
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// handleUpload handles POST /api/upload (multipart/form-data, field "file").
// In S3 mode: stores the object in S3 and returns a presigned GET URL.
// In fallback mode: saves to the local upload directory and returns a path.
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

	token, err := randomPrefix()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	safeName := sanitizeFilename(header.Filename)
	objectKey := token + "_" + safeName

	var result UploadResult
	result.Name = header.Filename

	if s.s3 != nil {
		url, written, err := s.uploadToS3(r, objectKey, file, header.Size)
		if err != nil {
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
		result.URL = url
		result.Size = written
	} else {
		path, written, err := s.uploadToFilesystem(objectKey, file)
		if err != nil {
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
		result.Path = path
		result.Size = written
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// uploadToS3 streams the file to S3 and returns a presigned GET URL.
func (s *Server) uploadToS3(r *http.Request, key string, file io.Reader, size int64) (url string, written int64, err error) {
	// Buffer the body so we have a known Content-Length for PutObject.
	buf, err := io.ReadAll(io.LimitReader(file, maxUploadSize+1))
	if err != nil {
		return "", 0, err
	}
	written = int64(len(buf))

	_, err = s.s3.client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:        aws.String(s.s3.cfg.Bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(buf),
		ContentLength: aws.Int64(written),
	})
	if err != nil {
		return "", 0, fmt.Errorf("s3 put: %w", err)
	}

	presigned, err := s.s3.presign.PresignGetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(s.s3.cfg.Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(s.s3.cfg.PresignTTL))
	if err != nil {
		return "", 0, fmt.Errorf("s3 presign: %w", err)
	}

	return presigned.URL, written, nil
}

// uploadToFilesystem saves the file locally — dev/single-node fallback only.
func (s *Server) uploadToFilesystem(name string, file io.Reader) (path string, written int64, err error) {
	dir := envOr("WEBD_UPLOAD_DIR", "/tmp/mcp-workspace/"+uploadSubDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", 0, err
	}
	destPath := filepath.Join(dir, name)
	dst, err := os.Create(destPath)
	if err != nil {
		return "", 0, err
	}
	defer dst.Close()

	n, err := io.Copy(dst, file)
	if err != nil {
		os.Remove(destPath)
		return "", 0, err
	}
	return destPath, n, nil
}

func sanitizeFilename(name string) string {
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
