package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"hop.top/kit/go/storage/blob"
)

// Store implements blob.Store using Amazon S3.
type Store struct {
	client *s3.Client
	bucket string
	prefix string
}

var _ blob.Store = (*Store)(nil)

// New returns a Store that reads/writes objects under prefix in bucket.
func New(client *s3.Client, bucket, prefix string) *Store {
	return &Store{client: client, bucket: bucket, prefix: prefix}
}

func (s *Store) key(k string) string { return path.Join(s.prefix, k) }

// Put writes r to the object at key with the given content type.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         aws.String(s.key(key)),
		Body:        r,
		ContentType: &contentType,
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

// Get returns a reader for the object at key.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    aws.String(s.key(key)),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	return out.Body, nil
}

// Delete removes the object at key.
func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    aws.String(s.key(key)),
	})
	if err != nil {
		return fmt.Errorf("s3 delete %s: %w", key, err)
	}
	return nil
}

// List returns metadata for all objects whose key starts with prefix.
func (s *Store) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	full := s.key(prefix)
	var objects []blob.Object

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: &s.bucket,
		Prefix: &full,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3 list %s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if s.prefix != "" {
				key = strings.TrimPrefix(key, s.prefix+"/")
			}
			objects = append(objects, blob.Object{
				Key:  key,
				Size: aws.ToInt64(obj.Size),
			})
		}
	}
	return objects, nil
}

// Exists reports whether an object exists at key.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    aws.String(s.key(key)),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return false, nil
		}
		return false, fmt.Errorf("s3 exists %s: %w", key, err)
	}
	return true, nil
}
