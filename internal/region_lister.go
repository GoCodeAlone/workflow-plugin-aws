package internal

import (
	"context"
	"sort"

	pb "github.com/GoCodeAlone/workflow/plugin/external/proto"
)

var awsProviderRegions = []string{
	"ap-northeast-1", "ap-southeast-1", "ap-southeast-2",
	"eu-central-1", "eu-west-1",
	"us-east-1", "us-east-2", "us-west-1", "us-west-2",
}

func (s *awsIaCServer) ListRegions(context.Context, *pb.ListRegionsRequest) (*pb.ListRegionsResponse, error) {
	regions := make([]string, len(awsProviderRegions))
	copy(regions, awsProviderRegions)
	sort.Strings(regions)

	out := make([]*pb.ProviderRegion, 0, len(regions))
	for _, name := range regions {
		out = append(out, &pb.ProviderRegion{Name: name, DisplayName: name})
	}
	return &pb.ListRegionsResponse{Regions: out}, nil
}
