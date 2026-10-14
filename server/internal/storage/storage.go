// Package storage keeps blobs on local disk or S3-compatible storage.
package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

var ErrNotFound = errors.New("blob not found")

type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

type Local struct {
	Dir string
}

func (l Local) paths(key string) (root, path string, err error) {
	if l.Dir == "" || key == "" || strings.Contains(key, `\`) || filepath.IsAbs(key) || filepath.VolumeName(key) != "" {
		return "", "", fmt.Errorf("bad storage key %q", key)
	}
	parts := strings.Split(key, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", "", fmt.Errorf("bad storage key %q", key)
		}
	}
	root, err = filepath.Abs(l.Dir)
	if err != nil {
		return "", "", fmt.Errorf("resolve storage directory: %w", err)
	}
	if err = os.MkdirAll(root, 0o700); err != nil {
		return "", "", fmt.Errorf("create storage directory: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve storage directory symlinks: %w", err)
	}
	path = filepath.Join(append([]string{root}, parts...)...)
	return root, path, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (l Local) Put(ctx context.Context, key string, r io.Reader, _ int64) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	rootPath, path, err := l.paths(key)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open storage directory: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close storage directory: %w", err))
		}
	}()
	relative, err := filepath.Rel(rootPath, path)
	if err != nil || !within(rootPath, path) {
		return fmt.Errorf("storage key %q escapes storage directory", key)
	}
	parent := filepath.Dir(relative)
	if err := root.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create blob directory: %w", err)
	}
	var temp string
	var file *os.File
	for range 10 {
		suffix := make([]byte, 12)
		if _, err := rand.Read(suffix); err != nil {
			return fmt.Errorf("generate temporary blob name: %w", err)
		}
		temp = filepath.Join(parent, ".put-"+hex.EncodeToString(suffix))
		file, err = root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("create temporary blob: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if err := root.Remove(temp); err != nil && !errors.Is(err, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove temporary blob: %w", err))
			}
		}
	}()
	if _, err := io.Copy(file, contextReader{ctx: ctx, reader: r}); err != nil {
		retErr = errors.Join(fmt.Errorf("write blob: %w", err), file.Close())
		return retErr
	}
	if err := ctx.Err(); err != nil {
		retErr = errors.Join(err, file.Close())
		return retErr
	}
	if err := file.Sync(); err != nil {
		retErr = errors.Join(fmt.Errorf("sync blob: %w", err), file.Close())
		return retErr
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary blob: %w", err)
	}
	if err := root.Rename(temp, relative); err != nil {
		return fmt.Errorf("replace blob: %w", err)
	}
	committed = true
	dir, err := root.Open(parent)
	if err != nil {
		return fmt.Errorf("open blob directory for sync: %w", err)
	}
	if err := dir.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync blob directory: %w", err), dir.Close())
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("close blob directory: %w", err)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (l Local) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, p, err := l.paths(key)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("resolve blob path: %w", err)
	}
	if !within(root, resolved) {
		return nil, fmt.Errorf("storage key %q escapes storage directory", key)
	}
	f, err := os.Open(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open blob: %w", err)
	}
	return f, nil
}

type S3 struct {
	Client *s3.Client
	Bucket string
}

func NewS3(ctx context.Context, bucket, endpoint, region string) (*S3, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load S3 configuration: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})
	return &S3{Client: client, Bucket: bucket}, nil
}

func (s *S3) CreateBucket(ctx context.Context) error {
	_, err := s.Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.Bucket)})
	if err != nil {
		return fmt.Errorf("create S3 bucket: %w", err)
	}
	return nil
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if key == "" || size < 0 {
		return fmt.Errorf("invalid blob key or size")
	}
	_, err := s.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key), Body: r, ContentLength: aws.Int64(size)})
	if err != nil {
		return fmt.Errorf("put S3 blob: %w", err)
	}
	return nil
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		var apiErr smithy.APIError
		if errors.As(err, &noSuchKey) || (errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound")) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get S3 blob: %w", err)
	}
	return out.Body, nil
}
