package aws

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

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

func (c *AWSClient) UploadLogs(ctx context.Context, body io.Reader) (string, error) {
	key := fmt.Sprintf("%d.log", time.Now().UnixMilli())

	if _, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String("logs/" + key),
		Body:   body,
	}); err != nil {
		return "", err
	}

	return key, nil
}

func (c *AWSClient) GetLogs(ctx context.Context, key string) (io.Reader, error) {
	resp, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String("logs/" + key),
	})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}
