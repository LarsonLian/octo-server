package file

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"
)

// ServiceCOS 腾讯云COS文件上传（通过S3兼容协议）
type ServiceCOS struct {
	log.Log
	ctx            *config.Context
	downloadClient *http.Client
}

// NewServiceCOS NewServiceCOS
func NewServiceCOS(ctx *config.Context) *ServiceCOS {
	return &ServiceCOS{
		Log: log.NewTLog("FileCOS"),
		ctx: ctx,
		downloadClient: &http.Client{
			Timeout: time.Second * 30,
		},
	}
}

// withPrefix 拼接环境前缀到对象路径（多环境共用 bucket 时隔离路径）
func (sc *ServiceCOS) withPrefix(objectPath string) string {
	prefix := strings.TrimSpace(sc.ctx.GetConfig().COS.Prefix)
	if prefix == "" {
		return objectPath
	}
	return path.Join(prefix, objectPath)
}

// getClient builds a COS client targeted at the *server-internal* default
// endpoint (`cos.<region>.myqcloud.com`). It is used by UploadFile / GetFile
// — i.e. anywhere the Go process itself initiates the request — where the
// canonical SDK endpoint is the right thing to hit.
//
// Browser-facing presigned URLs MUST instead be issued by `newPublicClient`
// so the SigV4 signature is valid for the host the browser actually
// resolves. SigV4 covers `host` in the signed headers, so any post-sign
// host change would invalidate the signature — this is the same hazard
// MinIO closed at PR#50 R3+, mirrored here for COS.
func (sc *ServiceCOS) getClient() (*minio.Client, error) {
	cosConfig := sc.ctx.GetConfig().COS
	endpoint := fmt.Sprintf("cos.%s.myqcloud.com", cosConfig.Region)

	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cosConfig.SecretID, cosConfig.SecretKey, ""),
		Secure:       true,
		BucketLookup: minio.BucketLookupDNS, // COS 要求 virtual-hosted-style: <bucket>.cos.<region>.myqcloud.com
	})
	if err != nil {
		return nil, fmt.Errorf("创建COS客户端失败: %w", err)
	}
	return client, nil
}

// publicEndpoint resolves the browser-facing parent domain used to issue
// presigned URLs. COS uses virtual-hosted-style addressing
// (`<bucket>.<host>/<key>`), so the value passed to the minio SDK is the
// *parent* domain WITHOUT the bucket subdomain — the SDK adds the bucket
// prefix back when constructing the signed request URL.
//
// Resolution order:
//
//  1. `cosConfig.BucketURL` — the documented browser-facing endpoint.
//     Operators using a custom domain (CNAME, CDN, or alternate COS
//     region hostname) MUST set this. The host is expected to start with
//     `<Bucket>.` per the documented shape (e.g.
//     `https://my-bucket-12345678.cos.example.com`); that prefix is
//     stripped here so what we hand the SDK is the parent domain
//     (`cos.example.com`). The SDK's BucketLookupDNS then re-prefixes
//     `my-bucket-12345678.` and the resulting URL host matches BucketURL
//     exactly. No post-sign host rewrite is performed — the URL is
//     signed against the host the browser will actually hit.
//
//  2. Default SDK endpoint `cos.<region>.myqcloud.com` — used when
//     BucketURL is empty. The SDK virtual-hosts to
//     `<bucket>.cos.<region>.myqcloud.com`, which is the COS canonical
//     shape and is reachable from the browser when deployers do not
//     stand up a custom domain.
//
// Limitation — non-bucket-prefixed BucketURL: a custom CDN / accelerator
// domain that does NOT carry a `<bucket>.` subdomain (e.g.
// `BucketURL=https://cdn.example.com`) is currently not supported. The
// SDK is configured with `BucketLookupDNS` (line 66 / 160 below), so it
// will virtual-host the bucket back onto whatever host we hand it,
// producing `https://<bucket>.cdn.example.com` — a hostname that does
// not exist in DNS. If your CDN routes `cdn.example.com/<bucket>/...`
// in path-style instead, drop BucketURL (use the default SDK endpoint)
// and front it with a plain reverse proxy, or open a tracker to add a
// `BucketLookupPath` override knob here. The supported BucketURL shapes
// are documented in `configs/tsdd.yaml` and the `docker/octo` config.
//
// Returned `host` is the bare host[:port] suitable for `minio.New` (no
// scheme, no path). `secure` reflects the URL scheme — `http://` flips it
// to false so HTTP-only deployments (e.g. local emulators) do not get
// silently upgraded to HTTPS.
func (sc *ServiceCOS) publicEndpoint() (host string, secure bool) {
	cosConfig := sc.ctx.GetConfig().COS
	defaultHost := fmt.Sprintf("cos.%s.myqcloud.com", cosConfig.Region)

	base := strings.TrimSpace(cosConfig.BucketURL)
	if base == "" {
		return defaultHost, true
	}
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || parsed == nil || parsed.Host == "" {
		sc.Warn("cos.bucketURL 解析失败，回退到默认 COS 域名", zap.String("bucketURL", base))
		return defaultHost, true
	}

	h := parsed.Host
	// Strip the documented `<bucket>.` subdomain so that BucketLookupDNS
	// can re-prefix it without producing `<bucket>.<bucket>.cos...`.
	if cosConfig.Bucket != "" {
		bucketPrefix := cosConfig.Bucket + "."
		h = strings.TrimPrefix(h, bucketPrefix)
	}
	if h == "" {
		// Bucket-name-only host (no parent domain) is degenerate and not
		// a valid endpoint. Fall back to the default.
		sc.Warn("cos.bucketURL 仅包含 bucket 子域，无父域可用作签名 endpoint，回退到默认 COS 域名",
			zap.String("bucketURL", base))
		return defaultHost, true
	}
	secure = !strings.EqualFold(parsed.Scheme, "http")
	return h, secure
}

// newPublicClient builds a COS client signing against the browser-facing
// endpoint resolved by `publicEndpoint`. Presigned PUT/GET URLs MUST be
// issued from this client: SigV4 covers `host` in the signed headers, so
// any post-sign host rewrite invalidates the signature. Signing once with
// the public host means the URL the browser receives is the URL the
// signature is valid for — no rewriting needed. Same hazard MinIO closed
// at PR#50 R3+; this is the COS-side mirror.
//
// `Region` is set explicitly so the SDK skips a GetBucketLocation
// preflight on first use — that preflight is the wrong thing to do for
// pure URL signing (it is network I/O against a host the test
// environment cannot resolve), and once skipped the presign path
// becomes deterministic / offline.
func (sc *ServiceCOS) newPublicClient() (*minio.Client, error) {
	cosConfig := sc.ctx.GetConfig().COS
	host, secure := sc.publicEndpoint()
	region := strings.TrimSpace(cosConfig.Region)
	if region == "" {
		// SDK default; only reached if operator left region blank.
		region = "us-east-1"
	}
	client, err := minio.New(host, &minio.Options{
		Creds:        credentials.NewStaticV4(cosConfig.SecretID, cosConfig.SecretKey, ""),
		Secure:       secure,
		Region:       region,
		BucketLookup: minio.BucketLookupDNS,
	})
	if err != nil {
		return nil, fmt.Errorf("创建COS公网客户端失败: %w", err)
	}
	return client, nil
}

// UploadFile 上传文件到腾讯云COS
func (sc *ServiceCOS) UploadFile(filePath string, contentType string, contentDisposition string, copyFileWriter func(io.Writer) error) (map[string]interface{}, error) {
	buff := bytes.NewBuffer(make([]byte, 0))
	err := copyFileWriter(buff)
	if err != nil {
		sc.Error("复制文件内容失败！", zap.Error(err))
		return nil, err
	}

	cosConfig := sc.ctx.GetConfig().COS
	client, err := sc.getClient()
	if err != nil {
		return nil, err
	}

	bucketName := cosConfig.Bucket
	// COS 单 bucket 模式：保留完整路径（含 chat/ 等原始 bucket 名），用 prefix 区分环境
	fileName := sc.withPrefix(filePath)

	opts := minio.PutObjectOptions{
		ContentType: contentType,
		PartSize:    10 * 1024 * 1024,
	}
	if contentDisposition != "" {
		opts.ContentDisposition = contentDisposition
	}

	ctx := context.Background()
	n, err := client.PutObject(ctx, bucketName, fileName, buff, int64(buff.Len()), opts)
	if err != nil {
		sc.Error("上传文件到COS失败", zap.Error(err))
		return map[string]interface{}{
			"path": "",
		}, err
	}

	return map[string]interface{}{
		"path": n.Key,
	}, nil
}

// DownloadURL 获取COS文件下载地址
func (sc *ServiceCOS) GetFile(ph string) (io.ReadCloser, string, error) {
	client, err := sc.getClient()
	if err != nil {
		return nil, "", err
	}

	cosConfig := sc.ctx.GetConfig().COS
	bucketName := cosConfig.Bucket
	// COS 单 bucket 模式：保留完整路径，用 prefix 区分环境
	objectPath := sc.withPrefix(ph)

	obj, err := client.GetObject(context.Background(), bucketName, objectPath, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", err
	}
	stat, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, "", err
	}
	return obj, stat.ContentType, nil
}

// PresignedPutURL 生成预签名 PUT URL，用于客户端直传 COS。
//
// The returned URL is signed against the *browser-facing* endpoint
// resolved by `publicEndpoint`, not the SDK's default
// `cos.<region>.myqcloud.com`. SigV4 covers `host` in the signed
// headers, so any post-sign host change would invalidate the signature
// (R6→R7 fix: previously we signed against the default endpoint and
// then rewrote `presigned.Host` / `presigned.Scheme` to BucketURL,
// which produced `403 SignatureDoesNotMatch` from the COS gateway on
// every browser PUT — same hazard MinIO closed at PR#50 R3+, mirrored
// here for COS).
//
// fileSize is signed into the canonical-headers section as
// `Content-Length`. The browser MUST echo the same value (browsers
// compute it automatically from the request body length); any
// mismatch is rejected by COS as 403 SignatureDoesNotMatch — same
// enforcement model as the MinIO backend, see service_minio.go for
// the rationale.
func (sc *ServiceCOS) PresignedPutURL(objectPath string, contentType string, contentDisposition string, fileSize int64, expires time.Duration) (uploadURL string, downloadURL string, err error) {
	if fileSize <= 0 {
		return "", "", fmt.Errorf("预签名上传必须提供正向的 fileSize（字节数），用于在签名中固定 Content-Length")
	}
	cosConfig := sc.ctx.GetConfig().COS
	client, err := sc.newPublicClient()
	if err != nil {
		return "", "", err
	}

	key := sc.withPrefix(objectPath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	headers := http.Header{}
	headers.Set("Content-Length", strconv.FormatInt(fileSize, 10))
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	if contentDisposition != "" {
		headers.Set("Content-Disposition", contentDisposition)
	}
	presigned, err := client.PresignHeader(ctx, http.MethodPut, cosConfig.Bucket, key, expires, nil, headers)
	if err != nil {
		return "", "", fmt.Errorf("生成预签名URL失败: %w", err)
	}

	// No post-sign URL mutation — the public client above already signed
	// against `BucketURL`'s host, so what the SDK returns is exactly what
	// the browser must hit for the signature to validate.
	uploadURL = presigned.String()

	downloadURL, dlErr := sc.DownloadURL(objectPath, "")
	if dlErr != nil {
		sc.Warn("生成下载URL失败", zap.Error(dlErr))
	}
	return uploadURL, downloadURL, nil
}

// extractFilenameFromDisposition 从 Content-Disposition 头中提取文件名。
// 优先解析 RFC 5987 的 filename*=UTF-8”xxx 格式，其次解析 filename="xxx" 格式。
func extractFilenameFromDisposition(cd string) string {
	if cd == "" {
		return ""
	}

	// 优先匹配 filename*=UTF-8''xxx
	if idx := strings.Index(cd, "filename*=UTF-8''"); idx >= 0 {
		val := cd[idx+len("filename*=UTF-8''"):]
		// 截取到分号或末尾
		if semi := strings.Index(val, ";"); semi >= 0 {
			val = val[:semi]
		}
		val = strings.TrimSpace(val)
		if decoded, err := url.PathUnescape(val); err == nil && decoded != "" {
			return decoded
		}
	}

	// 回退：匹配 filename="xxx"
	if idx := strings.Index(cd, "filename=\""); idx >= 0 {
		val := cd[idx+len("filename=\""):]
		if end := strings.Index(val, "\""); end >= 0 {
			return val[:end]
		}
	}

	return ""
}

func (sc *ServiceCOS) DownloadURL(ph string, filename string) (string, error) {
	cosConfig := sc.ctx.GetConfig().COS

	downloadBase := cosConfig.BucketURL
	if strings.TrimSpace(downloadBase) == "" {
		downloadBase = fmt.Sprintf("https://%s.cos.%s.myqcloud.com", cosConfig.Bucket, cosConfig.Region)
	}

	ph = sc.withPrefix(ph)
	result, _ := url.JoinPath(downloadBase, ph)
	return result, nil
}

// PresignedGetURL 生成预签名 GET URL，带 response-content-disposition 用于下载。
//
// Like PresignedPutURL, the URL is signed against the browser-facing
// endpoint (`publicEndpoint` → `cosConfig.BucketURL`). No post-sign host
// rewrite is performed: the SigV4 signature covers `host`, so any
// mutation after signing would produce 403 SignatureDoesNotMatch from
// the COS gateway. R6→R7 fix mirrors the PUT-side change above.
func (sc *ServiceCOS) PresignedGetURL(objectPath string, filename string, disposition string, expires time.Duration) (string, error) {
	cosConfig := sc.ctx.GetConfig().COS
	client, err := sc.newPublicClient()
	if err != nil {
		return "", err
	}

	key := sc.withPrefix(objectPath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if disposition != "inline" {
		disposition = "attachment"
	}
	encodedFilename := "UTF-8''" + rfc5987Encode(filename)
	params := url.Values{}
	params.Set("response-content-disposition", fmt.Sprintf("%s; filename*=%s", disposition, encodedFilename))

	presigned, err := client.PresignHeader(ctx, http.MethodGet, cosConfig.Bucket, key, expires, params, nil)
	if err != nil {
		return "", fmt.Errorf("生成预签名GET URL失败: %w", err)
	}

	// No post-sign URL mutation — see PresignedPutURL for the rationale.
	return presigned.String(), nil
}
