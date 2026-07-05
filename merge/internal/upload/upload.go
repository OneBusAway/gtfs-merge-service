package upload

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Uploader struct {
	client   *s3.Client
	bucket   string
	endpoint string
}

func New(accessKeyID, secretAccessKey, endpoint, bucket string) (*Uploader, error) {
	customResolver := aws.EndpointResolverWithOptionsFunc(
		func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			if service == s3.ServiceID {
				return aws.Endpoint{
					URL:               endpoint,
					HostnameImmutable: true,
				}, nil
			}
			return aws.Endpoint{}, &aws.EndpointNotFoundError{}
		})

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("auto"),
		config.WithEndpointResolverWithOptions(customResolver),
		config.WithCredentialsProvider(aws.CredentialsProviderFunc(
			func(ctx context.Context) (aws.Credentials, error) {
				return aws.Credentials{
					AccessKeyID:     accessKeyID,
					SecretAccessKey: secretAccessKey,
				}, nil
			})),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(cfg)

	return &Uploader{
		client:   client,
		bucket:   bucket,
		endpoint: endpoint,
	}, nil
}

// UploadFile uploads the file at filePath to key with ContentType
// "application/zip" (used for the merged GTFS bundle).
func (u *Uploader) UploadFile(filePath, key string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer func() {
		_ = file.Close()
	}()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	fmt.Printf("Uploading %s (%.2f MB) to s3://%s/%s\n",
		filePath, float64(fileInfo.Size())/(1024*1024), u.bucket, key)

	return u.upload(file, fileInfo.Size(), key, "application/zip")
}

// UploadBytes uploads data (e.g. a generated report.json) to key with the
// given contentType.
func (u *Uploader) UploadBytes(data []byte, key, contentType string) error {
	fmt.Printf("Uploading %d bytes to s3://%s/%s\n", len(data), u.bucket, key)
	return u.upload(bytes.NewReader(data), int64(len(data)), key, contentType)
}

// ObjectURL returns the path-style URL of an object in this uploader's
// bucket (endpoint/bucket/key) — the same addressing shape upload() PUTs to.
// Used for the per-build URLs in bundle-inputs.json.
func (u *Uploader) ObjectURL(key string) string {
	return fmt.Sprintf("%s/%s/%s", strings.TrimRight(u.endpoint, "/"), u.bucket, key)
}

func (u *Uploader) upload(body io.Reader, size int64, key, contentType string) error {
	_, err := u.client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:        aws.String(u.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	})

	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	fmt.Printf("Successfully uploaded to %s\n", u.endpoint)
	return nil
}
