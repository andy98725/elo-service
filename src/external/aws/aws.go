package aws

import (
	"bytes"
	"context"
	"encoding/json"
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

// PutSpectateChunk writes one sequenced chunk of a match's spectator
// stream to S3 at live/<matchID>/<seq>.bin. Chunks are opaque bytes — the
// game-server picks the format. See manifest at live/<matchID>/manifest.json
// for the cursor a spectator needs to walk forward.
func (c *AWSClient) PutSpectateChunk(ctx context.Context, matchID string, seq int, data []byte) error {
	key := fmt.Sprintf("live/%s/%d.bin", matchID, seq)
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucketName),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
	})
	return err
}

// PutSpectateManifest overwrites live/<matchID>/manifest.json with the
// caller-provided JSON. Spectator clients GET the manifest to learn the
// latest_seq before issuing chunk reads.
func (c *AWSClient) PutSpectateManifest(ctx context.Context, matchID string, manifest []byte) error {
	key := fmt.Sprintf("live/%s/manifest.json", matchID)
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucketName),
		Key:           aws.String(key),
		Body:          bytes.NewReader(manifest),
		ContentLength: aws.Int64(int64(len(manifest))),
		ContentType:   aws.String("application/json"),
	})
	return err
}

// GetSpectateManifest tries replay/ first (post-EndMatch) then live/
// (in-progress). ErrNotFound only when neither prefix has the match.
func (c *AWSClient) GetSpectateManifest(ctx context.Context, matchID string) ([]byte, error) {
	if data, err := c.GetObject(ctx, fmt.Sprintf("replay/%s/manifest.json", matchID)); err == nil {
		return data, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return c.GetObject(ctx, fmt.Sprintf("live/%s/manifest.json", matchID))
}

// GetSpectateChunk applies the same replay-then-live preference as the
// manifest. Slice 4 guarantees finalized replay/ manifests are only
// written after every chunk has been copied, so a spectator that sees
// the replay manifest will find each chunk seq it points at.
func (c *AWSClient) GetSpectateChunk(ctx context.Context, matchID string, seq int) ([]byte, error) {
	if data, err := c.GetObject(ctx, fmt.Sprintf("replay/%s/%d.bin", matchID, seq)); err == nil {
		return data, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return c.GetObject(ctx, fmt.Sprintf("live/%s/%d.bin", matchID, seq))
}

// MoveSpectateLiveToReplay implements the live→replay rotation. Order
// matters for the spectator concurrency model:
//   1. Read live manifest (we need chunk_count to enumerate).
//   2. Copy every live/<matchID>/<seq>.bin to replay/<matchID>/<seq>.bin.
//   3. Write replay/<matchID>/manifest.json with finalized=true.
//      Now spectators see replay's manifest first; replay reads succeed.
//   4. Delete the live/ chunks + live/ manifest.
// A failure mid-2 leaves duplicate copies but that's harmless. A failure
// after 3 leaves orphan live/ objects that should be cleaned up by an
// out-of-band sweep — log loud and move on.
func (c *AWSClient) MoveSpectateLiveToReplay(ctx context.Context, matchID string) error {
	manifestKey := fmt.Sprintf("live/%s/manifest.json", matchID)
	manifestBytes, err := c.GetObject(ctx, manifestKey)
	if err != nil {
		// No live manifest = nothing to move. Common case for
		// non-spectate matches and matches that ended before any
		// chunks were written.
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return fmt.Errorf("read live manifest: %w", err)
	}

	var m struct {
		MatchID    string `json:"match_id"`
		StartedAt  string `json:"started_at"`
		LatestSeq  int    `json:"latest_seq"`
		ChunkCount int    `json:"chunk_count"`
		Finalized  bool   `json:"finalized"`
	}
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return fmt.Errorf("parse live manifest: %w", err)
	}

	for seq := 0; seq < m.ChunkCount; seq++ {
		src := fmt.Sprintf("%s/live/%s/%d.bin", c.bucketName, matchID, seq)
		dst := fmt.Sprintf("replay/%s/%d.bin", matchID, seq)
		if _, err := c.s3.CopyObject(ctx, &s3.CopyObjectInput{
			Bucket:     aws.String(c.bucketName),
			CopySource: aws.String(src),
			Key:        aws.String(dst),
		}); err != nil {
			return fmt.Errorf("copy chunk %d: %w", seq, err)
		}
	}

	m.Finalized = true
	finalizedManifest, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal finalized manifest: %w", err)
	}
	if _, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucketName),
		Key:           aws.String(fmt.Sprintf("replay/%s/manifest.json", matchID)),
		Body:          bytes.NewReader(finalizedManifest),
		ContentLength: aws.Int64(int64(len(finalizedManifest))),
		ContentType:   aws.String("application/json"),
	}); err != nil {
		return fmt.Errorf("write replay manifest: %w", err)
	}

	// Past this point, spectators read from replay/. Delete the live/
	// versions; failure here leaks storage but doesn't break correctness.
	for seq := 0; seq < m.ChunkCount; seq++ {
		_, _ = c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(c.bucketName),
			Key:    aws.String(fmt.Sprintf("live/%s/%d.bin", matchID, seq)),
		})
	}
	_, _ = c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(manifestKey),
	})
	return nil
}

// MatchArtifactMeta is the per-artifact metadata serialized into
// artifacts/<matchID>/index.json. Defined here (the leaf package) so
// both server.StorageService and any consumer can reference it without
// a circular import — server already imports aws to construct the
// AWSClient.
type MatchArtifactMeta struct {
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	UploadedAt  string `json:"uploaded_at"`
}

// PutMatchArtifact stores raw bytes at artifacts/<matchID>/<name> with
// the supplied Content-Type, then updates artifacts/<matchID>/index.json
// to record the new artifact's metadata. The R-M-W on index.json is
// non-atomic; concurrent uploads to the same match can lose one
// another's metadata entry. Game servers typically upload sequentially
// at end-of-match, so this is acceptable for v1.
func (c *AWSClient) PutMatchArtifact(ctx context.Context, matchID, name, contentType string, body []byte) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	objectKey := fmt.Sprintf("artifacts/%s/%s", matchID, name)
	if _, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucketName),
		Key:           aws.String(objectKey),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
		ContentType:   aws.String(contentType),
	}); err != nil {
		return fmt.Errorf("put artifact: %w", err)
	}

	indexKey := fmt.Sprintf("artifacts/%s/index.json", matchID)
	index := map[string]MatchArtifactMeta{}
	if existing, err := c.GetObject(ctx, indexKey); err == nil {
		_ = json.Unmarshal(existing, &index)
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("read artifact index: %w", err)
	}
	index[name] = MatchArtifactMeta{
		ContentType: contentType,
		SizeBytes:   int64(len(body)),
		UploadedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("marshal artifact index: %w", err)
	}
	if _, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucketName),
		Key:           aws.String(indexKey),
		Body:          bytes.NewReader(indexBytes),
		ContentLength: aws.Int64(int64(len(indexBytes))),
		ContentType:   aws.String("application/json"),
	}); err != nil {
		return fmt.Errorf("write artifact index: %w", err)
	}
	return nil
}

// GetMatchArtifact returns the raw bytes and Content-Type recorded by
// PutMatchArtifact. The Content-Type comes from S3's object metadata,
// not the index — both should agree but the per-object header is the
// authoritative one for HTTP response headers.
func (c *AWSClient) GetMatchArtifact(ctx context.Context, matchID, name string) ([]byte, string, error) {
	resp, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(fmt.Sprintf("artifacts/%s/%s", matchID, name)),
	})
	if err != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	contentType := ""
	if resp.ContentType != nil {
		contentType = *resp.ContentType
	}
	return body, contentType, nil
}

// GetMatchArtifactIndex returns the parsed metadata for every artifact
// in this match. Returns an empty map (not nil, not error) when the
// match has no artifacts — callers can range over the result without
// nil-checking.
func (c *AWSClient) GetMatchArtifactIndex(ctx context.Context, matchID string) (map[string]MatchArtifactMeta, error) {
	indexKey := fmt.Sprintf("artifacts/%s/index.json", matchID)
	data, err := c.GetObject(ctx, indexKey)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return map[string]MatchArtifactMeta{}, nil
		}
		return nil, err
	}
	out := map[string]MatchArtifactMeta{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse artifact index: %w", err)
	}
	return out, nil
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
