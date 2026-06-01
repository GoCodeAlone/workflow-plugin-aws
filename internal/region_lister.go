package internal

import (
	"context"
	"sort"

	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

var awsFallbackRegions = []string{
	"af-south-1",
	"ap-east-1",
	"ap-east-2",
	"ap-northeast-1",
	"ap-northeast-2",
	"ap-northeast-3",
	"ap-south-1",
	"ap-south-2",
	"ap-southeast-1",
	"ap-southeast-2",
	"ap-southeast-3",
	"ap-southeast-4",
	"ap-southeast-5",
	"ap-southeast-7",
	"ca-central-1",
	"ca-west-1",
	"eu-central-1",
	"eu-central-2",
	"eu-north-1",
	"eu-south-1",
	"eu-south-2",
	"eu-west-1",
	"eu-west-2",
	"eu-west-3",
	"il-central-1",
	"me-central-1",
	"me-south-1",
	"mx-central-1",
	"sa-east-1",
	"us-east-1",
	"us-east-2",
	"us-west-1",
	"us-west-2",
}

func (s *awsIaCServer) ListRegions(ctx context.Context, _ *pb.ListRegionsRequest) (*pb.ListRegionsResponse, error) {
	if s != nil && s.provider != nil {
		cfg, ok := s.provider.AWSConfigSnapshot()
		if ok {
			regions, err := listAWSRegions(ctx, cfg)
			if err != nil {
				return nil, err
			}
			return providerRegionsResponse(regions), nil
		}
	}
	return providerRegionsResponse(awsFallbackRegions), nil
}

func listAWSRegions(ctx context.Context, cfg aws.Config) ([]string, error) {
	resp, err := ec2.NewFromConfig(cfg).DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: aws.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	regions := make([]string, 0, len(resp.Regions))
	for _, region := range resp.Regions {
		if region.RegionName != nil && *region.RegionName != "" {
			regions = append(regions, *region.RegionName)
		}
	}
	return regions, nil
}

func providerRegionsResponse(regions []string) *pb.ListRegionsResponse {
	regions = append([]string(nil), regions...)
	sort.Strings(regions)

	out := make([]*pb.ProviderRegion, 0, len(regions))
	for _, name := range regions {
		out = append(out, &pb.ProviderRegion{Name: name, DisplayName: name})
	}
	return &pb.ListRegionsResponse{Regions: out}
}
