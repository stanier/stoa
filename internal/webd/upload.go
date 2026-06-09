package webd

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

// allowedMIMETypes is the set of content types accepted by the upload handler.
// SVG and HTML are intentionally excluded — they can carry inline scripts.
var allowedMIMETypes = map[string]bool{
	"image/jpeg":       true,
	"image/png":        true,
	"image/gif":        true,
	"image/webp":       true,
	"application/pdf":  true,
	"text/plain":       true,
	"application/json": true,
}

// UploadResult is returned to the browser after a successful upload.
// AttachmentID is an opaque HMAC-signed token (S3 mode) that the agent passes
// to the read_attachment MCP tool — the agent never sees a URL or S3 key.
// Path is set only in local-filesystem fallback mode (dev/single-node).
type UploadResult struct {
	AttachmentID string `json:"attachment_id,omitempty"` // opaque token (S3 mode)
	Path         string `json:"path,omitempty"`          // local filesystem path (fallback mode)
	Name         string `json:"name"`
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

	// Detect content type from the first 512 bytes and validate against the allowlist.
	// Use the file bytes rather than the declared Content-Type header — browsers lie.
	sniff := make([]byte, 512)
	n, _ := file.Read(sniff)
	detectedType := http.DetectContentType(sniff[:n])
	// DetectContentType may return "text/plain; charset=utf-8" — strip parameters.
	mimeBase := strings.SplitN(detectedType, ";", 2)[0]
	if !allowedMIMETypes[mimeBase] {
		http.Error(w, fmt.Sprintf("file type %q not allowed", mimeBase), http.StatusUnsupportedMediaType)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
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
		attachmentID, written, err := s.uploadToS3(r, objectKey, file, header.Size)
		if err != nil {
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
		result.AttachmentID = attachmentID
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

// uploadToS3 stores the file in S3 and returns an opaque HMAC-signed attachment ID.
// The ID encodes the S3 key and a MAC so the mcp-attachments server can validate
// and resolve it without any shared database.
func (s *Server) uploadToS3(r *http.Request, key string, file io.Reader, size int64) (attachmentID string, written int64, err error) {
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

	return makeAttachmentID(s.cfg.AttachmentSecret, key), written, nil
}

// makeAttachmentID returns a compact signed token: hex(hmac[:8]) + "." + base64url(s3_key).
// The mcp-attachments server verifies the MAC before resolving the key — preventing
// the agent from requesting arbitrary S3 objects.
func makeAttachmentID(secret []byte, s3Key string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(s3Key))
	macHex := hex.EncodeToString(mac.Sum(nil)[:8])
	keyEnc := base64.RawURLEncoding.EncodeToString([]byte(s3Key))
	return macHex + "." + keyEnc
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

// resolveS3Attachment validates the HMAC-signed attachmentID and downloads the
// object from S3, returning its raw bytes. Returns an error if the MAC is invalid.
func (s *Server) resolveS3Attachment(ctx context.Context, attachmentID string) ([]byte, error) {
	if s.s3 == nil {
		return nil, fmt.Errorf("S3 not configured")
	}
	parts := strings.SplitN(attachmentID, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed attachment id")
	}
	keyBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("malformed attachment id: %w", err)
	}
	s3Key := string(keyBytes)

	mac := hmac.New(sha256.New, s.cfg.AttachmentSecret)
	mac.Write([]byte(s3Key))
	if expected := hex.EncodeToString(mac.Sum(nil)[:8]); expected != parts[0] {
		return nil, fmt.Errorf("invalid attachment id")
	}

	out, err := s.s3.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.s3.cfg.Bucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return nil, fmt.Errorf("S3 get: %w", err)
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

// resolveLocalAttachment reads an uploaded file from the local upload directory.
// The path is validated to be within the configured upload dir.
func resolveLocalAttachment(path string) ([]byte, error) {
	uploadDir := envOr("WEBD_UPLOAD_DIR", "/tmp/mcp-workspace/"+uploadSubDir)
	absPath := filepath.Clean(path)
	dirWithSep := uploadDir + string(filepath.Separator)
	if absPath != uploadDir && !strings.HasPrefix(absPath, dirWithSep) {
		return nil, fmt.Errorf("path outside upload directory")
	}
	return os.ReadFile(absPath)
}
