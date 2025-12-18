package csi

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type s3DeleteConfig struct {
	Endpoint     string
	Region       string
	AllowHTTP    bool
	AccessKeyID  string
	SecretKey    string
	UsePathStyle bool
}

type s3Location struct {
	Bucket string
	Prefix string
}

func parseS3Location(storageURL string) (s3Location, error) {
	u, err := url.Parse(storageURL)
	if err != nil {
		return s3Location{}, err
	}
	if u.Scheme != "s3" {
		return s3Location{}, fmt.Errorf("unsupported storageURL scheme %q (expected s3)", u.Scheme)
	}
	bucket := u.Host
	if bucket == "" {
		return s3Location{}, fmt.Errorf("storageURL missing bucket: %q", storageURL)
	}
	prefix := strings.TrimPrefix(u.Path, "/")
	return s3Location{Bucket: bucket, Prefix: prefix}, nil
}

func joinS3Prefix(basePrefix, volumeID string) string {
	basePrefix = strings.Trim(strings.TrimSpace(basePrefix), "/")
	volumeID = strings.Trim(strings.TrimSpace(volumeID), "/")
	if basePrefix == "" {
		return volumeID
	}
	return path.Join(basePrefix, volumeID)
}

func deleteS3Prefix(ctx context.Context, cfg s3DeleteConfig, loc s3Location) error {
	if cfg.Endpoint == "" {
		return fmt.Errorf("missing aws endpoint")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if !cfg.AllowHTTP && strings.HasPrefix(cfg.Endpoint, "http://") {
		return fmt.Errorf("refusing to use insecure endpoint %q with awsAllowHTTP=false", cfg.Endpoint)
	}
	if loc.Bucket == "" {
		return fmt.Errorf("missing bucket")
	}
	if strings.TrimSpace(loc.Prefix) == "" {
		return fmt.Errorf("refusing to delete empty prefix in bucket %q", loc.Bucket)
	}

	awsCfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretKey, "")),
	)
	if err != nil {
		return err
	}

	awsCfg.EndpointResolverWithOptions = aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...any) (aws.Endpoint, error) {
			if service == s3.ServiceID {
				return aws.Endpoint{
					URL:               cfg.Endpoint,
					HostnameImmutable: true,
				}, nil
			}
			return aws.Endpoint{}, &aws.EndpointNotFoundError{}
		},
	)

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.UsePathStyle
	})

	p := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(loc.Bucket),
		Prefix: aws.String(strings.TrimSpace(loc.Prefix)),
	})

	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		if len(page.Contents) == 0 {
			continue
		}

		// DeleteObjects supports up to 1000 keys.
		objs := make([]types.ObjectIdentifier, 0, len(page.Contents))
		for _, obj := range page.Contents {
			if obj.Key == nil || *obj.Key == "" {
				continue
			}
			objs = append(objs, types.ObjectIdentifier{Key: obj.Key})
		}

		input := &s3.DeleteObjectsInput{
			Bucket: aws.String(loc.Bucket),
			Delete: &types.Delete{
				Objects: objs,
				Quiet:   aws.Bool(true),
			},
		}

		if _, err := client.DeleteObjects(ctx, input); err != nil {
			return err
		}
	}

	return nil
}
