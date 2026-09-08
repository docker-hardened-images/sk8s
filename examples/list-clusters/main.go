// Command list-clusters lists EKS cluster names in eu-central-1 using the same
// credential chain as sk8s's LoadConfig (see eks.go).
//
// Usage from repository root:
//
//	go run ./examples/list-clusters
//
// Requires IAM permission eks:ListClusters for the account in that region.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	sk8s "github.com/docker-hardened-images/sk8s"
)

func main() {
	ctx := context.Background()

	cfg, err := sk8s.LoadConfig(ctx)
	if err != nil {
		log.Fatalf("load aws config: %v", err)
	}

	client := sk8s.NewEKSClient(cfg)
	names, err := client.ListClusters(ctx)
	if err != nil {
		log.Fatalf("list clusters: %v", err)
	}

	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "no EKS clusters in eu-central-1 (or none visible to this principal)")
		return
	}

	firstCluster := names[0]
	for _, name := range names {
		fmt.Println(name)
	}

	// describe the first cluster endpoint
	summary, err := client.DescribeCluster(ctx, firstCluster)
	if err != nil {
		log.Fatalf("describe cluster %s: %v", firstCluster, err)
	}
	fmt.Println(summary.Endpoint)
}
