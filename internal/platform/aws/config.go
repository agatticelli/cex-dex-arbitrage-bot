package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

// Config holds AWS configuration
type Config struct {
	Region string
}

// LoadAWSConfig loads AWS SDK configuration using default credential chain
// (environment variables, shared credentials file, IAM roles, etc.)
func LoadAWSConfig(ctx context.Context, cfg Config) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
}
