// Package s3store implements PhotoStore on any S3-compatible object store
// (AWS S3, MinIO, Cloudflare R2, GCS interop).
package s3store

import (
	"context"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Store struct {
	Client *minio.Client
	Bucket string
}

// New connects and makes sure the bucket exists.
func New(ctx context.Context, endpoint, accessKey, secretKey, bucket, region string, useSSL bool) (*Store, error) {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL, Region: region,
	})
	if err != nil {
		return nil, err
	}
	ok, err := c.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: region}); err != nil {
			return nil, err
		}
	}
	return &Store{Client: c, Bucket: bucket}, nil
}

func (s *Store) Put(ctx context.Context, key, contentType string, r io.Reader, size int64) error {
	_, err := s.Client.PutObject(ctx, s.Bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	o, err := s.Client.GetObject(ctx, s.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", err
	}
	st, err := o.Stat()
	if err != nil {
		o.Close()
		return nil, "", err
	}
	return o, st.ContentType, nil
}
