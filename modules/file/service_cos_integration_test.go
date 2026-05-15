package file_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/Mininglamp-OSS/octo-server/modules/file"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCOSPresignedURLs_SignAgainstPublicEndpoint mirrors the MinIO-side
// integration test (see service_minio_integration_test.go) for COS.
//
// PR#50 R6 shipped a `presigned.Host = bucketURL.Host` mutation AFTER
// signing — same hazard MinIO closed at R3+. SigV4 covers `host` in
// the signed headers, so any post-sign host change produces 403
// SignatureDoesNotMatch from the COS gateway on every browser PUT/GET.
//
// The R7 fix builds a public-facing minio client whose endpoint is
// derived from `cosConfig.BucketURL` (parent domain after stripping
// the documented `<bucket>.` subdomain), and signs against that
// client directly. Reading the resulting URL host back out and
// confirming it matches BucketURL is equivalent to confirming the
// signature is valid for that host: if the URL host disagreed with
// the host actually signed, the URL would not authenticate at the
// COS gateway.
//
// The test uses fake credentials and never makes a network call —
// PresignHeader / PresignedGetObject are pure URL signing.
func TestCOSPresignedURLs_SignAgainstPublicEndpoint(t *testing.T) {
	cfg := config.New()
	cfg.Test = true
	cfg.COS.SecretID = "test-secret-id"
	cfg.COS.SecretKey = "test-secret-key-1234567890"
	cfg.COS.Bucket = "my-bucket-12345678"
	cfg.COS.Region = "ap-beijing"
	cfg.COS.BucketURL = "https://my-bucket-12345678.cos.example.com"

	ctx := testutil.NewTestContext(cfg)
	svc := file.NewServiceCOS(ctx)

	t.Run("PUT URL signed against public host (no rewriting)", func(t *testing.T) {
		uploadURL, downloadURL, err := svc.PresignedPutURL(
			"chat/2026/05/abc.jpg", "image/jpeg", "", 12345, 5*time.Minute,
		)
		require.NoError(t, err)
		require.NotEmpty(t, uploadURL)
		require.NotEmpty(t, downloadURL)

		u, err := url.Parse(uploadURL)
		require.NoError(t, err)

		// Host check: BucketURL host should match exactly. The minio
		// SDK virtual-hosts `<bucket>.<parent>` — with parent
		// `cos.example.com` and bucket `my-bucket-12345678`, the
		// reconstructed host equals BucketURL host.
		assert.Equal(t, "my-bucket-12345678.cos.example.com", u.Host,
			"presigned PUT URL must be served from the BucketURL host, got %s", u.Host)
		assert.Equal(t, "https", u.Scheme,
			"presigned PUT URL must inherit scheme from BucketURL")

		// SigV4 shape: `host` and `content-length` MUST appear in
		// the signed headers. Because the signing client was
		// constructed against BucketURL's parent domain, the host
		// covered by the signature is the URL's own host. Any
		// post-sign host change would break that invariant.
		// `content-length` is the P0 size-bypass guard landed in R6.
		q := u.Query()
		assert.NotEmpty(t, q.Get("X-Amz-Signature"),
			"presigned PUT URL must carry a SigV4 signature")
		signedHeaders := q.Get("X-Amz-SignedHeaders")
		assert.Contains(t, signedHeaders, "host",
			"presigned PUT URL must include `host` in its signed headers (got %q)", signedHeaders)
		assert.Contains(t, signedHeaders, "content-length",
			"presigned PUT URL must include `content-length` in its signed headers so the COS gateway can enforce the upload size cap (got %q)", signedHeaders)
	})

	t.Run("GET URL signed against public host (no rewriting)", func(t *testing.T) {
		raw, err := svc.PresignedGetURL("chat/2026/05/abc.jpg", "report.jpg", "attachment", 5*time.Minute)
		require.NoError(t, err)
		require.NotEmpty(t, raw)

		u, err := url.Parse(raw)
		require.NoError(t, err)

		assert.Equal(t, "my-bucket-12345678.cos.example.com", u.Host,
			"presigned GET URL must be served from the BucketURL host, got %s", u.Host)
		assert.Equal(t, "https", u.Scheme,
			"presigned GET URL must inherit scheme from BucketURL")

		q := u.Query()
		assert.NotEmpty(t, q.Get("X-Amz-Signature"),
			"presigned GET URL must carry a SigV4 signature")
		assert.NotEmpty(t, q.Get("X-Amz-Credential"),
			"presigned GET URL must carry the SigV4 credential scope")
		signedHeaders := q.Get("X-Amz-SignedHeaders")
		assert.Contains(t, signedHeaders, "host",
			"presigned GET URL must include `host` in its signed headers (got %q)", signedHeaders)

		assert.True(t,
			strings.Contains(u.Path, "/chat/") && strings.HasSuffix(u.Path, "/abc.jpg"),
			"object path should be reflected in the signed URL, got %s", u.Path)

		disposition := q.Get("response-content-disposition")
		assert.Contains(t, disposition, "attachment",
			"response-content-disposition should preserve the requested disposition")
		assert.Contains(t, disposition, "report.jpg",
			"response-content-disposition should carry the requested filename")
	})
}

// TestCOSPresignedURLs_DefaultEndpointWhenBucketURLEmpty pins the
// fallback contract: when `cosConfig.BucketURL` is empty, presigned
// URLs are signed against the SDK's canonical endpoint
// `<bucket>.cos.<region>.myqcloud.com`. This is the COS "no custom
// domain" deployment shape — the canonical hostname is reachable
// from the browser without any operator-side DNS work.
func TestCOSPresignedURLs_DefaultEndpointWhenBucketURLEmpty(t *testing.T) {
	cfg := config.New()
	cfg.Test = true
	cfg.COS.SecretID = "test-secret-id"
	cfg.COS.SecretKey = "test-secret-key-1234567890"
	cfg.COS.Bucket = "my-bucket-12345678"
	cfg.COS.Region = "ap-beijing"
	cfg.COS.BucketURL = "" // fallback path

	svc := file.NewServiceCOS(testutil.NewTestContext(cfg))

	uploadURL, _, err := svc.PresignedPutURL(
		"chat/2026/05/abc.jpg", "image/jpeg", "", 12345, time.Minute,
	)
	require.NoError(t, err)

	u, err := url.Parse(uploadURL)
	require.NoError(t, err)
	assert.Equal(t, "my-bucket-12345678.cos.ap-beijing.myqcloud.com", u.Host,
		"with BucketURL empty, presigned URL must use canonical COS host")
	assert.Equal(t, "https", u.Scheme,
		"COS canonical endpoint must be HTTPS")
}

// TestCOSPresignedURLs_WithPrefix pins the env-prefix routing: when
// `cosConfig.Prefix` is set (multi-env shared bucket), the prefix
// is prepended to the object key BEFORE signing, so the signed URL
// resolves to the prefixed object on the COS server. This is the
// behaviour `withPrefix` provided in R6 and the R7 host fix must
// not regress.
func TestCOSPresignedURLs_WithPrefix(t *testing.T) {
	cfg := config.New()
	cfg.Test = true
	cfg.COS.SecretID = "test-secret-id"
	cfg.COS.SecretKey = "test-secret-key-1234567890"
	cfg.COS.Bucket = "my-bucket-12345678"
	cfg.COS.Region = "ap-beijing"
	cfg.COS.BucketURL = "https://my-bucket-12345678.cos.example.com"
	cfg.COS.Prefix = "env-test-prefix"

	svc := file.NewServiceCOS(testutil.NewTestContext(cfg))

	uploadURL, _, err := svc.PresignedPutURL(
		"chat/2026/05/abc.jpg", "image/jpeg", "", 12345, time.Minute,
	)
	require.NoError(t, err)

	u, err := url.Parse(uploadURL)
	require.NoError(t, err)
	assert.Equal(t, "my-bucket-12345678.cos.example.com", u.Host,
		"prefix routing must not perturb the BucketURL host")
	assert.Contains(t, u.Path, "/env-test-prefix/chat/2026/05/abc.jpg",
		"signed URL path must include the env prefix, got %s", u.Path)
}

// TestCOSPresignedURLs_HTTPScheme pins that an `http://` BucketURL is
// honoured (non-TLS local emulators or test setups). Going via the
// SDK's `Secure: false` toggle means the signature is computed for
// the http variant — flipping to https post-sign would invalidate it.
func TestCOSPresignedURLs_HTTPScheme(t *testing.T) {
	cfg := config.New()
	cfg.Test = true
	cfg.COS.SecretID = "test-secret-id"
	cfg.COS.SecretKey = "test-secret-key-1234567890"
	cfg.COS.Bucket = "my-bucket-12345678"
	cfg.COS.Region = "ap-beijing"
	cfg.COS.BucketURL = "http://my-bucket-12345678.cos.local"

	svc := file.NewServiceCOS(testutil.NewTestContext(cfg))

	uploadURL, _, err := svc.PresignedPutURL(
		"chat/2026/05/abc.jpg", "image/jpeg", "", 12345, time.Minute,
	)
	require.NoError(t, err)

	u, err := url.Parse(uploadURL)
	require.NoError(t, err)
	assert.Equal(t, "http", u.Scheme, "http BucketURL must produce http presigned URL")
	assert.Equal(t, "my-bucket-12345678.cos.local", u.Host)
}
