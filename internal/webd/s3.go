package webd

import (
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Config holds S3-compatible object storage configuration for webd.
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	PresignTTL      time.Duration
}

// s3Clients bundles the plain S3 client and the presign client together.
type s3Clients struct {
	client  *s3.Client
	presign *s3.PresignClient
	cfg     S3Config
}

// newS3Clients builds an S3 client pair from the given config.
// PathStyle is forced on because Garage (and most self-hosted S3) requires it.
func newS3Clients(cfg S3Config) (*s3Clients, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("webd: S3 endpoint is required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("webd: S3 bucket is required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("webd: S3 access key ID and secret access key are required")
	}

	region := cfg.Region
	if region == "" {
		region = "garage"
	}
	ttl := cfg.PresignTTL
	if ttl == 0 {
		ttl = 7 * 24 * time.Hour
	}
	cfg.Region = region
	cfg.PresignTTL = ttl

	awsCfg := aws.Config{
		Region: region,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		),
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = true
	})

	return &s3Clients{
		client:  client,
		presign: s3.NewPresignClient(client),
		cfg:     cfg,
	}, nil
}
