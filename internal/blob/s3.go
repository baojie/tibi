package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"tibi/internal/disposition"
	"tibi/internal/id"
)

// S3Config configures an S3-compatible backend (AWS S3, Backblaze B2,
// Cloudflare R2, MinIO, Wasabi, …).
type S3Config struct {
	// Endpoint is a host[:port], optionally with an http:// or https://
	// scheme (default https).
	Endpoint     string
	Region       string
	Bucket       string
	Prefix       string // key prefix inside the bucket, e.g. "blobs/"
	AccessKey    string
	SecretKey    string
	UsePathStyle bool // MinIO and most self-hosted servers need this
}

// S3 stores blobs as private objects in a bucket.
type S3 struct {
	c      *minio.Client
	bucket string
	prefix string
}

// NewS3 connects to the bucket. It does not verify access; see Probe.
func NewS3(cfg S3Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("s3: endpoint and bucket are required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("s3: access_key and secret_key are required (env TIBI_S3_ACCESS_KEY / TIBI_S3_SECRET_KEY)")
	}
	endpoint, secure := cfg.Endpoint, true
	switch {
	case strings.HasPrefix(endpoint, "http://"):
		endpoint, secure = strings.TrimPrefix(endpoint, "http://"), false
	case strings.HasPrefix(endpoint, "https://"):
		endpoint = strings.TrimPrefix(endpoint, "https://")
	}
	endpoint = strings.TrimRight(endpoint, "/")
	lookup := minio.BucketLookupAuto
	if cfg.UsePathStyle {
		lookup = minio.BucketLookupPath
	}
	c, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       secure,
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}
	prefix := strings.TrimLeft(cfg.Prefix, "/")
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &S3{c: c, bucket: cfg.Bucket, prefix: prefix}, nil
}

func (s *S3) obj(key string) (string, error) {
	if key == "" || strings.Contains(key, "..") || strings.HasPrefix(key, "/") {
		return "", fmt.Errorf("blob: invalid key %q", key)
	}
	return s.prefix + key, nil
}

func notFound(err error) bool {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.Code == "NoSuchKey" || resp.StatusCode == 404
	}
	return false
}

// Put uploads r as a private object with immutable caching headers.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, mt string) error {
	name, err := s.obj(key)
	if err != nil {
		return err
	}
	if mt == "" {
		mt = "application/octet-stream"
	}
	_, err = s.c.PutObject(ctx, s.bucket, name, r, size, minio.PutObjectOptions{
		ContentType:  mt,
		CacheControl: "public, max-age=31536000, immutable",
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

// Open returns a seekable reader (Range requests seek within the object).
func (s *S3) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	name, err := s.obj(key)
	if err != nil {
		return nil, err
	}
	o, err := s.c.GetObject(ctx, s.bucket, name, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	// GetObject is lazy; Stat surfaces a missing key now.
	if _, err := o.Stat(); err != nil {
		o.Close()
		if notFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	return o, nil
}

// Delete removes the object; a missing key is not an error.
//
// On versioned buckets (Backblaze B2 keeps every version by default, as do
// AWS buckets with versioning on) a plain DELETE only hides the object
// behind a delete marker and the bytes stay billed, so every version of the
// key is removed explicitly.
func (s *S3) Delete(ctx context.Context, key string) error {
	name, err := s.obj(key)
	if err != nil {
		return err
	}
	var versions []string
	lctx, cancel := context.WithCancel(ctx)
	for o := range s.c.ListObjects(lctx, s.bucket, minio.ListObjectsOptions{Prefix: name, WithVersions: true, Recursive: true}) {
		if o.Err != nil {
			versions = nil // listing versions unsupported: plain delete below
			break
		}
		if o.Key == name && o.VersionID != "" && o.VersionID != "null" {
			versions = append(versions, o.VersionID)
		}
	}
	cancel()
	for _, v := range versions {
		if err := s.c.RemoveObject(ctx, s.bucket, name, minio.RemoveObjectOptions{VersionID: v}); err != nil && !notFound(err) {
			return fmt.Errorf("s3 delete %s (version %s): %w", key, v, err)
		}
	}
	if err := s.c.RemoveObject(ctx, s.bucket, name, minio.RemoveObjectOptions{}); err != nil && !notFound(err) {
		return fmt.Errorf("s3 delete %s: %w", key, err)
	}
	if len(versions) > 0 {
		// The plain delete above may itself have left a marker; remove it.
		lctx, cancel := context.WithCancel(ctx)
		defer cancel()
		for o := range s.c.ListObjects(lctx, s.bucket, minio.ListObjectsOptions{Prefix: name, WithVersions: true, Recursive: true}) {
			if o.Err == nil && o.Key == name && o.VersionID != "" && o.VersionID != "null" {
				s.c.RemoveObject(ctx, s.bucket, name, minio.RemoveObjectOptions{VersionID: o.VersionID})
			}
		}
	}
	return nil
}

// Exists reports whether the object is present.
func (s *S3) Exists(ctx context.Context, key string) (bool, error) {
	name, err := s.obj(key)
	if err != nil {
		return false, err
	}
	_, err = s.c.StatObject(ctx, s.bucket, name, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if notFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("s3 stat %s: %w", key, err)
}

// List calls fn for each object under prefix.
func (s *S3) List(ctx context.Context, prefix string, fn func(Info) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for o := range s.c.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: s.prefix + prefix, Recursive: true}) {
		if o.Err != nil {
			return fmt.Errorf("s3 list: %w", o.Err)
		}
		key := strings.TrimPrefix(o.Key, s.prefix)
		if strings.HasPrefix(key, probePrefix) {
			continue
		}
		if err := fn(Info{Key: key, Size: o.Size, ModTime: o.LastModified}); err != nil {
			return err
		}
	}
	return nil
}

// PresignGet returns a time-limited URL that serves the object with the
// given type, as an attachment named filename when filename is set.
func (s *S3) PresignGet(ctx context.Context, key string, ttl time.Duration, filename, mt string) (string, bool, error) {
	name, err := s.obj(key)
	if err != nil {
		return "", false, err
	}
	params := url.Values{}
	if mt != "" {
		params.Set("response-content-type", mt)
	}
	if filename != "" {
		params.Set("response-content-disposition", disposition.Attachment(filename, "download"+path.Ext(filename)))
	}
	u, err := s.c.PresignedGetObject(ctx, s.bucket, name, ttl, params)
	if err != nil {
		return "", false, fmt.Errorf("s3 presign %s: %w", key, err)
	}
	return u.String(), true, nil
}

const probePrefix = "_probe/"

// Probe checks that the bucket accepts writes, reads and deletes, so a
// misconfigured server fails at startup instead of on the first upload.
func Probe(ctx context.Context, s Store) error {
	key := probePrefix + id.Random(16)
	data := []byte("tibi startup probe")
	if err := s.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		return fmt.Errorf("storage probe: write failed: %w", err)
	}
	ok, err := s.Exists(ctx, key)
	if err != nil || !ok {
		return fmt.Errorf("storage probe: object not found after write (err=%v)", err)
	}
	f, err := s.Open(ctx, key)
	if err != nil {
		return fmt.Errorf("storage probe: read failed: %w", err)
	}
	got, err := io.ReadAll(f)
	f.Close()
	if err != nil || !bytes.Equal(got, data) {
		return fmt.Errorf("storage probe: read back wrong data (err=%v)", err)
	}
	if err := s.Delete(ctx, key); err != nil {
		return fmt.Errorf("storage probe: delete failed: %w", err)
	}
	return nil
}
