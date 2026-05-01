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
	PresignTTL      time.Duration // kept for potential future use; not used by upload path
}

// s3Clients bundles the S3 client used for PutObject calls.
type s3Clients struct {
	client *s3.Client
	cfg    S3Config
}

// newS3Clients builds an S3 client from the given config.
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
	cfg.Region = region

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
		client: client,
		cfg:    cfg,
	}, nil
}
