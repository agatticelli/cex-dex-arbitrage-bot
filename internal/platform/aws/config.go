package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// Config holds AWS configuration
type Config struct {
	Region   string
	Endpoint string // For LocalStack
}

// LoadAWSConfig loads AWS SDK configuration
func LoadAWSConfig(ctx context.Context, cfg Config) (aws.Config, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(cfg.Region),
	}

	// Use static credentials for LocalStack
	if cfg.Endpoint != "" {
		opts = append(opts,
			config.WithEndpointResolverWithOptions(
				aws.EndpointResolverWithOptionsFunc(
					func(service, region string, options ...interface{}) (aws.Endpoint, error) {
						return aws.Endpoint{
							URL:           cfg.Endpoint,
							SigningRegion: cfg.Region,
						}, nil
					},
				),
			),
			config.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider("test", "test", ""),
			),
		)
	}

	return config.LoadDefaultConfig(ctx, opts...)
}
