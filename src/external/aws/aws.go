package aws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ErrNotFound is returned by GetObject when the requested key does not
// exist. The cert manager uses it to distinguish "no cert yet, please
// issue one" from real S3 errors.
var ErrNotFound = errors.New("aws: object not found")

type AWSClient struct {
	s3         *s3.Client
	bucketName string
}

func InitAWSClient(accessKeyID, secretAccessKey, region, bucketName string) (*AWSClient, error) {
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")),
		config.WithRegion(region),
	)
	if err != nil {
		return nil, err
	}

	client := &AWSClient{s3: s3.NewFromConfig(cfg), bucketName: bucketName}
	if _, err = client.s3.ListBuckets(context.Background(), &s3.ListBucketsInput{}); err != nil {
		return nil, err
	}

	return client, nil
}

func (c *AWSClient) UploadLogs(ctx context.Context, body []byte) (string, error) {
	key := fmt.Sprintf("%d.log", time.Now().UnixMilli())

	if _, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucketName),
		Key:           aws.String("logs/" + key),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
	}); err != nil {
		return "", err
	}

	return key, nil
}

func (c *AWSClient) GetLogs(ctx context.Context, key string) (io.ReadCloser, error) {
	resp, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String("logs/" + key),
	})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// PutObject and GetObject are the generic blob-storage surface used by the
// cert manager (full key, not the "logs/" prefix). GetObject returns
// ErrNotFound on a missing key so the cert manager can detect "no cert
// yet" without parsing SDK error types.
func (c *AWSClient) PutObject(ctx context.Context, key string, body []byte) error {
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucketName),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
	})
	return err
}

func (c *AWSClient) GetObject(ctx context.Context, key string) ([]byte, error) {
	resp, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
