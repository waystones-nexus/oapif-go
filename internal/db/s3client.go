package db

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	appconfig "github.com/waystones/oapif-go/internal/config"
)

// NewS3Client builds an AWS SDK v2 S3 client from the app config.
// Supports AWS S3, Cloudflare R2, and other S3-compatible stores.
func NewS3Client(cfg *appconfig.Config) *s3.Client {
	opts := s3.Options{
		Region: cfg.S3Region,
	}
	if cfg.S3AccessKey != "" {
		opts.Credentials = credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, "")
	}
	if cfg.S3Endpoint != "" {
		opts.BaseEndpoint = aws.String(cfg.S3Endpoint)
		opts.UsePathStyle = cfg.S3URLStyle == "path"
	}
	return s3.New(opts)
}
