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

var (
	_ blob.Store  = (*Store)(nil)
	_ blob.Stater = (*Store)(nil)
)

// New returns a Store that reads/writes objects under prefix in bucket.
// Trailing slashes on prefix are ignored, so "tool-data" and "tool-data/"
// address the same objects.
func New(client *s3.Client, bucket, prefix string) *Store {
	return &Store{client: client, bucket: bucket, prefix: strings.TrimRight(prefix, "/")}
}

func (s *Store) key(k string) string { return path.Join(s.prefix, k) }

// listPrefix returns the S3 key prefix for a caller-side List prefix. Unlike
// key it keeps a trailing slash: "logs/" must not match "logs-old/x".
func (s *Store) listPrefix(prefix string) string {
	if s.prefix == "" {
		return prefix
	}
	return s.prefix + "/" + prefix
}

// callerKey strips the store prefix from a full object key.
func (s *Store) callerKey(full string) string {
	if s.prefix == "" {
		return full
	}
	return strings.TrimPrefix(full, s.prefix+"/")
}

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

// Delete removes the object at key. Deleting a missing key succeeds (S3
// DeleteObject is idempotent).
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

// Stat returns the metadata of the object at key via HeadObject: size
// (ContentLength) and ContentType. A missing key returns an error wrapping
// the SDK's NotFound, as Get wraps NoSuchKey. Implements blob.Stater.
func (s *Store) Stat(ctx context.Context, key string) (blob.Object, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    aws.String(s.key(key)),
	})
	if err != nil {
		return blob.Object{}, fmt.Errorf("s3 stat %s: %w", key, err)
	}
	return blob.Object{
		Key:         key,
		Size:        aws.ToInt64(out.ContentLength),
		ContentType: aws.ToString(out.ContentType),
	}, nil
}

// List returns metadata for all objects whose key starts with prefix.
// ListObjectsV2 carries no content type, so Object.ContentType is empty;
// use Stat (blob.Stater) to read a type, one HEAD per object.
func (s *Store) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	full := s.listPrefix(prefix)
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
			key := s.callerKey(aws.ToString(obj.Key))
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
