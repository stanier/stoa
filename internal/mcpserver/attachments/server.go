package attachments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stanier/stoa/internal/mcpserver"
)

const maxReadBytes = 4 * 1024 * 1024 // 4 MB

// Config holds configuration for the attachments MCP server.
type Config struct {
	Secret          []byte // HMAC secret shared with webd (ATTACHMENT_SECRET)
	S3Endpoint      string
	S3Region        string
	S3Bucket        string
	S3AccessKeyID   string
	S3SecretKey     string
}

// Server is the MCP server that resolves opaque attachment IDs and returns file content.
type Server struct {
	mcpserver.BaseServer
	cfg      Config
	s3Client *s3.Client
	handlers map[string]mcpserver.ToolHandler
}

// New creates a new attachments MCP server.
func New(name, version string, cfg Config) (*Server, error) {
	if len(cfg.Secret) == 0 {
		return nil, errors.New("attachments: ATTACHMENT_SECRET is required")
	}
	if cfg.S3Endpoint == "" || cfg.S3Bucket == "" || cfg.S3AccessKeyID == "" || cfg.S3SecretKey == "" {
		return nil, errors.New("attachments: S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_KEY are required")
	}

	region := cfg.S3Region
	if region == "" {
		region = "garage"
	}

	awsCfg := aws.Config{
		Region: region,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.S3AccessKeyID,
			cfg.S3SecretKey,
			"",
		),
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.S3Endpoint)
		o.UsePathStyle = true
	})

	srv := &Server{
		BaseServer: mcpserver.BaseServer{Name: name, Version: version},
		cfg:        cfg,
		s3Client:   client,
		handlers:   make(map[string]mcpserver.ToolHandler),
	}
	srv.handlers["read_attachment"] = srv.handleReadAttachment
	return srv, nil
}

func (s *Server) ListTools(_ context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name: "read_attachment",
			Description: "Read the content of an uploaded file attachment. " +
				"Use this tool when a message contains an attachment reference like (attachment: ID). " +
				"Pass the attachment ID exactly as it appears — do not modify it.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"attachment_id": map[string]interface{}{
						"type":        "string",
						"description": "The opaque attachment ID from the message, e.g. 'deadbeef01234567.dXBsb2Fkcy9...'",
					},
				},
				"required": []string{"attachment_id"},
			},
		},
	}, nil
}

func (s *Server) Shutdown(_ context.Context) error { return nil }

func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	h, ok := s.handlers[name]
	if !ok {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	return h(ctx, arguments)
}

func (s *Server) handleReadAttachment(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "attachment_id", "")
	if id == "" {
		return mcpserver.ErrorResult("attachment_id is required"), nil
	}

	s3Key, err := parseAttachmentID(s.cfg.Secret, s.cfg.S3Bucket, id)
	if err != nil {
		return mcpserver.ErrorResult("invalid attachment ID: " + err.Error()), nil
	}

	out, err := s.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.S3Bucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not retrieve attachment: %v", err)), nil
	}
	defer out.Body.Close()

	data, err := io.ReadAll(io.LimitReader(out.Body, maxReadBytes+1))
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not read attachment: %v", err)), nil
	}

	truncated := ""
	if int64(len(data)) > maxReadBytes {
		data = data[:maxReadBytes]
		truncated = fmt.Sprintf("\n\n[truncated — file exceeds %d MB read limit]", maxReadBytes/1024/1024)
	}

	// Return as text; if the content is not valid UTF-8, base64-encode it.
	if !isValidUTF8(data) {
		enc := base64.StdEncoding.EncodeToString(data)
		return mcpserver.TextResult("(base64-encoded binary content)\n" + enc + truncated), nil
	}

	return mcpserver.TextResult(string(data) + truncated), nil
}

// parseAttachmentID validates the HMAC and returns the S3 key encoded in the ID.
// Format: hex(hmac_sha256(secret, bucket+":"+key)[:8]) + "." + base64url(key)
func parseAttachmentID(secret []byte, bucket, id string) (string, error) {
	parts := strings.SplitN(id, ".", 2)
	if len(parts) != 2 || len(parts[0]) != 16 {
		return "", errors.New("malformed attachment ID")
	}
	expectedMAC, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", errors.New("malformed attachment ID")
	}
	keyBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("malformed attachment ID")
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(bucket + ":" + string(keyBytes)))
	if !hmac.Equal(expectedMAC, mac.Sum(nil)[:8]) {
		return "", errors.New("signature mismatch")
	}
	return string(keyBytes), nil
}

func isValidUTF8(data []byte) bool {
	return utf8.Valid(data)
}
