package sk8s

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	// tokenPrefix identifies the token as an aws-iam-authenticator-compatible EKS bearer token,
	// per https://github.com/kubernetes-sigs/aws-iam-authenticator.
	tokenPrefix = "k8s-aws-v1."

	// clusterIDHeader is the extra signed header EKS's authenticator webhook requires on the
	// presigned request to scope the token to a single cluster.
	clusterIDHeader = "x-k8s-aws-id"

	// presignExpiresParam is the value sent for the (otherwise ignored by STS/EKS) X-Amz-Expires
	// parameter. The presigned URL is actually valid for 15 minutes from its X-Amz-Date signing
	// time regardless of this value; it is set to 60 only because older authenticator server
	// versions reject values outside 0-60.
	presignExpiresParam = 60

	// tokenLifetime is the window EKS enforces server-side, measured from the presigned
	// request's signing time.
	tokenLifetime = 15 * time.Minute
)

// GenerateToken returns a Kubernetes API bearer token for clusterName using the same
// STS-presigned-URL scheme as `aws eks get-token`, `eksctl`, and the client-go exec credential
// plugin shipped by aws-iam-authenticator ("k8s-aws-v1.<base64 presigned sts:GetCallerIdentity>",
// see https://github.com/kubernetes-sigs/aws-iam-authenticator/blob/master/pkg/token/token.go).
// This is the same scheme the EKS control plane's built-in authenticator validates, so the
// returned token works directly as a bearer token against the cluster's API server -- no
// aws-iam-authenticator binary or `aws` CLI needs to be installed.
//
// The token is valid for about 15 minutes from generation (EKS enforces this window server-side
// regardless of any requested expiry). Generating one performs no network call itself -- it is a
// local SigV4 presign -- so callers with long-running sessions should call GenerateToken again
// close to expiration rather than caching the result indefinitely.
func GenerateToken(ctx context.Context, cfg aws.Config, clusterName string) (token string, expiresAt time.Time, err error) {
	if clusterName == "" {
		return "", time.Time{}, fmt.Errorf("cluster name is required")
	}

	stsClient := sts.NewFromConfig(cfg)
	presignClient := sts.NewPresignClient(stsClient)

	presigned, err := presignClient.PresignGetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}, func(o *sts.PresignOptions) {
		o.ClientOptions = append(o.ClientOptions, func(stsOptions *sts.Options) {
			stsOptions.APIOptions = append(stsOptions.APIOptions,
				smithyhttp.SetHeaderValue(clusterIDHeader, clusterName),
				smithyhttp.SetHeaderValue("X-Amz-Expires", strconv.Itoa(presignExpiresParam)),
			)
		})
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("presign GetCallerIdentity for cluster %q: %w", clusterName, err)
	}

	tok := tokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(presigned.URL))

	// Cut a minute off the server-enforced window for cushion, mirroring aws-iam-authenticator.
	expiresAt = time.Now().Add(tokenLifetime - time.Minute)

	return tok, expiresAt, nil
}
