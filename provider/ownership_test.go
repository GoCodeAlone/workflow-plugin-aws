package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/GoCodeAlone/workflow/interfaces"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

type fakeOwnershipTaggingClient struct {
	getInputs   []*resourcegroupstaggingapi.GetResourcesInput
	getOutputs  []*resourcegroupstaggingapi.GetResourcesOutput
	tagInputs   []*resourcegroupstaggingapi.TagResourcesInput
	untagInputs []*resourcegroupstaggingapi.UntagResourcesInput
}

func (f *fakeOwnershipTaggingClient) GetResources(ctx context.Context, in *resourcegroupstaggingapi.GetResourcesInput, optFns ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	f.getInputs = append(f.getInputs, in)
	if len(f.getOutputs) == 0 {
		return &resourcegroupstaggingapi.GetResourcesOutput{}, nil
	}
	out := f.getOutputs[0]
	f.getOutputs = f.getOutputs[1:]
	return out, nil
}

func (f *fakeOwnershipTaggingClient) TagResources(ctx context.Context, in *resourcegroupstaggingapi.TagResourcesInput, optFns ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.TagResourcesOutput, error) {
	f.tagInputs = append(f.tagInputs, in)
	return &resourcegroupstaggingapi.TagResourcesOutput{}, nil
}

func (f *fakeOwnershipTaggingClient) UntagResources(ctx context.Context, in *resourcegroupstaggingapi.UntagResourcesInput, optFns ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.UntagResourcesOutput, error) {
	f.untagInputs = append(f.untagInputs, in)
	return &resourcegroupstaggingapi.UntagResourcesOutput{}, nil
}

func TestOwnershipProviderCompileGuard(t *testing.T) {
	var _ interfaces.OwnershipProvider = (*AWSProvider)(nil)
}

func TestSetOwnerTagsARNWithWorkflowOwnerKey(t *testing.T) {
	client := &fakeOwnershipTaggingClient{}
	p := initializedOwnershipProvider(client)
	arn := "arn:aws:ecs:us-east-1:123456789012:service/default/api"

	if err := p.SetOwner(context.Background(), interfaces.ResourceRef{Name: "api", Type: "infra.container_service", ProviderID: arn}, "workflow"); err != nil {
		t.Fatalf("SetOwner: %v", err)
	}

	if len(client.tagInputs) != 1 {
		t.Fatalf("TagResources calls = %d, want 1", len(client.tagInputs))
	}
	got := client.tagInputs[0]
	if len(got.ResourceARNList) != 1 || got.ResourceARNList[0] != arn {
		t.Fatalf("ResourceARNList = %v, want [%s]", got.ResourceARNList, arn)
	}
	if got.Tags[ownershipTagKey] != "workflow" {
		t.Fatalf("Tags[%q] = %q, want workflow", ownershipTagKey, got.Tags[ownershipTagKey])
	}
}

func TestSetOwnerRejectsNonARNProviderID(t *testing.T) {
	p := initializedOwnershipProvider(&fakeOwnershipTaggingClient{})

	err := p.SetOwner(context.Background(), interfaces.ResourceRef{Name: "vpc", Type: "infra.vpc", ProviderID: "vpc-123"}, "workflow")
	if err == nil {
		t.Fatal("SetOwner returned nil, want unsupported non-ARN error")
	}
	if !errors.Is(err, ErrOwnershipARNRequired) {
		t.Fatalf("SetOwner error = %v, want ErrOwnershipARNRequired", err)
	}
}

func TestGetOwnerReadsWorkflowOwnerTag(t *testing.T) {
	arn := "arn:aws:rds:us-east-1:123456789012:db:orders"
	client := &fakeOwnershipTaggingClient{
		getOutputs: []*resourcegroupstaggingapi.GetResourcesOutput{
			{
				ResourceTagMappingList: []tagtypes.ResourceTagMapping{
					{
						ResourceARN: awssdk.String(arn),
						Tags: []tagtypes.Tag{
							{Key: awssdk.String("Name"), Value: awssdk.String("orders")},
							{Key: awssdk.String(ownershipTagKey), Value: awssdk.String("payments")},
						},
					},
				},
			},
		},
	}
	p := initializedOwnershipProvider(client)

	owner, err := p.GetOwner(context.Background(), interfaces.ResourceRef{Name: "orders", Type: "infra.database", ProviderID: arn})
	if err != nil {
		t.Fatalf("GetOwner: %v", err)
	}
	if owner.Owner != "payments" {
		t.Fatalf("Owner = %q, want payments", owner.Owner)
	}
	if owner.Source != ownershipTagSource {
		t.Fatalf("Source = %q, want %q", owner.Source, ownershipTagSource)
	}
	if owner.Ref.ProviderID != arn {
		t.Fatalf("Ref.ProviderID = %q, want %q", owner.Ref.ProviderID, arn)
	}
}

func TestListOwnersMapsTaggedARNsAndFiltersResourceType(t *testing.T) {
	ecsARN := "arn:aws:ecs:us-east-1:123456789012:service/default/api"
	rdsARN := "arn:aws:rds:us-east-1:123456789012:db:orders"
	client := &fakeOwnershipTaggingClient{
		getOutputs: []*resourcegroupstaggingapi.GetResourcesOutput{
			{
				ResourceTagMappingList: []tagtypes.ResourceTagMapping{
					{
						ResourceARN: awssdk.String(ecsARN),
						Tags:        []tagtypes.Tag{{Key: awssdk.String(ownershipTagKey), Value: awssdk.String("workflow")}},
					},
					{
						ResourceARN: awssdk.String(rdsARN),
						Tags:        []tagtypes.Tag{{Key: awssdk.String(ownershipTagKey), Value: awssdk.String("workflow")}},
					},
				},
			},
		},
	}
	p := initializedOwnershipProvider(client)

	owners, err := p.ListOwners(context.Background(), interfaces.OwnerFilter{Owner: "workflow", ResourceType: "infra.container_service"})
	if err != nil {
		t.Fatalf("ListOwners: %v", err)
	}
	if len(owners) != 1 {
		t.Fatalf("owners len = %d, want 1: %#v", len(owners), owners)
	}
	got := owners[0]
	if got.Owner != "workflow" || got.Source != ownershipTagSource {
		t.Fatalf("owner metadata = %#v, want owner workflow source %q", got, ownershipTagSource)
	}
	if got.Ref.Name != "api" || got.Ref.Type != "infra.container_service" || got.Ref.ProviderID != ecsARN {
		t.Fatalf("ref = %#v, want api infra.container_service %s", got.Ref, ecsARN)
	}
	if len(client.getInputs) != 1 {
		t.Fatalf("GetResources calls = %d, want 1", len(client.getInputs))
	}
	if len(client.getInputs[0].TagFilters) != 1 || awssdk.ToString(client.getInputs[0].TagFilters[0].Key) != ownershipTagKey {
		t.Fatalf("TagFilters = %#v, want %q filter", client.getInputs[0].TagFilters, ownershipTagKey)
	}
	if gotValues := client.getInputs[0].TagFilters[0].Values; len(gotValues) != 1 || gotValues[0] != "workflow" {
		t.Fatalf("TagFilter values = %v, want [workflow]", gotValues)
	}
}

func initializedOwnershipProvider(client ownershipTaggingClient) *AWSProvider {
	return &AWSProvider{
		initialized:     true,
		ownershipClient: client,
	}
}
