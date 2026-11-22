package repository

import (
	"context"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

// s3ClientParams 描述构造 S3 兼容客户端所需的参数。
type s3ClientParams struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
}

// newS3Client 构造一个 S3 兼容客户端，兼容 AWS S3 / Cloudflare R2 / 阿里云 OSS / MinIO。
//
// 通过 SwapComputePayloadSHA256ForUnsignedPayloadMiddleware + RequestChecksumCalculationWhenRequired
// 规避阿里云 OSS 不兼容 s3manager 分片签名的问题（backup 与 image storage 共用此构造）。
//
// 出站安全：
//   - 若调用方通过 context 注入了安全客户端（管理后台"测试连接"入口），
//     直接使用它：拨号前校验解析后的 IP、重定向每一跳都校验；
//   - 否则仍然拒绝 3xx 跳到私网/环回——公网 S3 endpoint 把请求重定向到内网
//     永远不是合法行为，而允许内网 MinIO 的部署照常可用（不注入拨号校验）。
func newS3Client(ctx context.Context, p s3ClientParams) (*s3.Client, error) {
	region := p.Region
	if region == "" {
		region = "auto" // Cloudflare R2 默认 region
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(p.AccessKeyID, p.SecretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	if client := service.ObjectStoreHTTPClientFromContext(ctx); client != nil {
		awsCfg.HTTPClient = client
	} else {
		awsCfg.HTTPClient = urlvalidator.RedirectGuardOnlyClient(http.DefaultTransport, urlvalidator.OutboundPolicy{})
	}

	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if p.Endpoint != "" {
			o.BaseEndpoint = &p.Endpoint
		}
		if p.ForcePathStyle {
			o.UsePathStyle = true
		}
		o.APIOptions = append(o.APIOptions, v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	}), nil
}
