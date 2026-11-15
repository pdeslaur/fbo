// Package r2 reads and writes an R2 bucket with temporary credentials from
// cloudflare-sts, which a GitHub Actions job gets in exchange for its OIDC
// token, so that the job holds no long-lived key.
package r2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Bucket is a bucket and the temporary credentials cloudflare-sts issued
// for it.
type Bucket struct {
	Name            string `json:"name"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token"`
	Endpoint        string `json:"endpoint"`
}

// GitHubToken returns a GitHub Actions job's OIDC token for audience. The
// job's requestURL and requestToken are its ACTIONS_ID_TOKEN_REQUEST_URL and
// ACTIONS_ID_TOKEN_REQUEST_TOKEN, which it has with the id-token: write
// permission.
func GitHubToken(ctx context.Context, requestURL, requestToken, audience string) (string, error) {
	if requestURL == "" || requestToken == "" {
		return "", errors.New("no OIDC token to request: the job needs the id-token: write permission")
	}
	u, err := url.Parse(requestURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("audience", audience)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+requestToken)
	var r struct {
		Value string `json:"value"`
	}
	if err := do(req, &r); err != nil {
		return "", fmt.Errorf("requesting the job's OIDC token: %w", err)
	}
	return r.Value, nil
}

// Exchange trades an OIDC token for the bucket of the cloudflare-sts profile
// scope, at broker, the broker's URL.
func Exchange(ctx context.Context, broker, scope, subjectToken string) (Bucket, error) {
	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {subjectToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:id_token"},
		"scope":              {scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(broker, "/")+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Bucket{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var r struct {
		Bucket *Bucket `json:"bucket"`
	}
	if err := do(req, &r); err != nil {
		return Bucket{}, fmt.Errorf("exchanging the OIDC token at %s: %w", broker, err)
	}
	if r.Bucket == nil {
		return Bucket{}, fmt.Errorf("%s gave no bucket for %s", broker, scope)
	}
	return *r.Bucket, nil
}

func do(req *http.Request, out any) error {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		// The broker explains a refusal in an OAuth error, which holds no
		// secret.
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(body))
	}
	return json.Unmarshal(body, out)
}

// ErrNotFound is Get's error for a key the bucket doesn't have.
var ErrNotFound = errors.New("not found")

// Object is an object in a bucket.
type Object struct {
	Key  string
	Size int64
}

// Store reads and writes a bucket.
type Store struct {
	client *s3.Client
	bucket string
}

// NewStore returns a Store for b with its credentials.
func NewStore(b Bucket) *Store {
	client := s3.New(s3.Options{
		Region:       "auto",
		BaseEndpoint: aws.String(b.Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(b.AccessKeyID, b.SecretAccessKey, b.SessionToken),
		UsePathStyle: true,
		// The SDK's default checksums aren't all R2's: Cloudflare's docs
		// have them sent and checked only when an operation requires them.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &Store{client: client, bucket: b.Name}
}

// Get returns an object's contents, or ErrNotFound.
func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, fmt.Errorf("%s: %w", key, ErrNotFound)
		}
		return nil, fmt.Errorf("getting %s: %w", key, err)
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

// Put writes an object, replacing any there.
func (s *Store) Put(ctx context.Context, key string, data []byte, contentType, cacheControl string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       &s.bucket,
		Key:          &key,
		Body:         bytes.NewReader(data),
		ContentType:  &contentType,
		CacheControl: &cacheControl,
	})
	if err != nil {
		return fmt.Errorf("putting %s: %w", key, err)
	}
	return nil
}

// PutIfAbsent writes an object unless the key exists, reporting whether it
// wrote it. The bucket checks, so two writers can't both create a key.
func (s *Store) PutIfAbsent(ctx context.Context, key string, data []byte, contentType, cacheControl string) (bool, error) {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       &s.bucket,
		Key:          &key,
		Body:         bytes.NewReader(data),
		ContentType:  &contentType,
		CacheControl: &cacheControl,
		IfNoneMatch:  aws.String("*"),
	})
	var re *awshttp.ResponseError
	if errors.As(err, &re) && re.HTTPStatusCode() == http.StatusPreconditionFailed {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("putting %s: %w", key, err)
	}
	return true, nil
}

// List returns the objects whose keys start with prefix.
func (s *Store) List(ctx context.Context, prefix string) ([]Object, error) {
	var objects []Object
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, err)
		}
		for _, o := range page.Contents {
			objects = append(objects, Object{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size)})
		}
	}
	return objects, nil
}
