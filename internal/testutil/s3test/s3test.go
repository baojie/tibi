// Package s3test configures S3 for tests from the environment:
//
//	TIBI_TEST_S3_ENDPOINT   e.g. http://127.0.0.1:19000 (unset: S3 tests skip)
//	TIBI_TEST_S3_ACCESS_KEY, TIBI_TEST_S3_SECRET_KEY
//	TIBI_TEST_S3_BUCKET     default "tibi-test" (created if missing)
//	TIBI_TEST_S3_REGION     default "us-east-1"
//
// Each test gets its own key prefix, removed when the test ends.
package s3test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"tibi/internal/blob"
	"tibi/internal/id"
)

// Enabled reports whether S3 tests are configured.
func Enabled() bool { return os.Getenv("TIBI_TEST_S3_ENDPOINT") != "" }

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// Config returns an S3 config with a fresh prefix, creating the bucket if
// needed and deleting the prefix's objects at cleanup. It skips the test
// when S3 is not configured.
func Config(t testing.TB) blob.S3Config {
	t.Helper()
	if !Enabled() {
		t.Skip("TIBI_TEST_S3_ENDPOINT not set")
	}
	cfg := blob.S3Config{
		Endpoint:     os.Getenv("TIBI_TEST_S3_ENDPOINT"),
		Region:       env("TIBI_TEST_S3_REGION", "us-east-1"),
		Bucket:       env("TIBI_TEST_S3_BUCKET", "tibi-test"),
		Prefix:       "test-" + strings.ToLower(id.Random(10)) + "/",
		AccessKey:    os.Getenv("TIBI_TEST_S3_ACCESS_KEY"),
		SecretKey:    os.Getenv("TIBI_TEST_S3_SECRET_KEY"),
		UsePathStyle: true,
	}
	c := client(t, cfg)
	ctx := context.Background()
	ok, err := c.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		t.Fatalf("s3: %v", err)
	}
	if !ok {
		if err := c.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			t.Fatalf("s3 make bucket: %v", err)
		}
	}
	t.Cleanup(func() {
		for o := range c.ListObjects(ctx, cfg.Bucket, minio.ListObjectsOptions{Prefix: cfg.Prefix, Recursive: true}) {
			if o.Err == nil {
				c.RemoveObject(ctx, cfg.Bucket, o.Key, minio.RemoveObjectOptions{})
			}
		}
	})
	return cfg
}

func client(t testing.TB, cfg blob.S3Config) *minio.Client {
	ep, secure := cfg.Endpoint, true
	if strings.HasPrefix(ep, "http://") {
		ep, secure = strings.TrimPrefix(ep, "http://"), false
	}
	ep = strings.TrimPrefix(ep, "https://")
	c, err := minio.New(ep, &minio.Options{
		Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: secure,
		Region: cfg.Region, BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
