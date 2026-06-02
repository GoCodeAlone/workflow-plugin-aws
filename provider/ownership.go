package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/GoCodeAlone/workflow/interfaces"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

const (
	ownershipTagKey    = "workflow-owner"
	ownershipTagSource = "tag:workflow-owner"
)

var ErrOwnershipARNRequired = errors.New("aws ownership requires ResourceRef.ProviderID to be an ARN")

type ownershipTaggingClient interface {
	GetResources(context.Context, *resourcegroupstaggingapi.GetResourcesInput, ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error)
	TagResources(context.Context, *resourcegroupstaggingapi.TagResourcesInput, ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.TagResourcesOutput, error)
	UntagResources(context.Context, *resourcegroupstaggingapi.UntagResourcesInput, ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.UntagResourcesOutput, error)
}

func (p *AWSProvider) GetOwner(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOwner, error) {
	p.mu.RLock()
	client, err := p.ownershipClientLocked()
	p.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	arn, err := ownershipARN(ref)
	if err != nil {
		return nil, err
	}
	out, err := client.GetResources(ctx, &resourcegroupstaggingapi.GetResourcesInput{ResourceARNList: []string{arn}})
	if err != nil {
		return nil, fmt.Errorf("aws: get ownership tags for %q: %w", ref.Name, err)
	}
	for _, mapping := range out.ResourceTagMappingList {
		if awssdk.ToString(mapping.ResourceARN) == arn {
			return &interfaces.ResourceOwner{Ref: ref, Owner: ownerFromTags(mapping.Tags), Source: ownershipTagSource}, nil
		}
	}
	return &interfaces.ResourceOwner{Ref: ref, Source: ownershipTagSource}, nil
}

func (p *AWSProvider) SetOwner(ctx context.Context, ref interfaces.ResourceRef, owner string) error {
	if strings.TrimSpace(owner) == "" {
		return fmt.Errorf("aws: owner must be non-empty")
	}
	p.mu.RLock()
	client, err := p.ownershipClientLocked()
	p.mu.RUnlock()
	if err != nil {
		return err
	}
	arn, err := ownershipARN(ref)
	if err != nil {
		return err
	}
	if _, err := client.TagResources(ctx, &resourcegroupstaggingapi.TagResourcesInput{
		ResourceARNList: []string{arn},
		Tags:            map[string]string{ownershipTagKey: owner},
	}); err != nil {
		return fmt.Errorf("aws: tag %s/%s with owner %q: %w", ref.Type, ref.Name, owner, err)
	}
	return nil
}

func (p *AWSProvider) ListOwners(ctx context.Context, filter interfaces.OwnerFilter) ([]interfaces.ResourceOwner, error) {
	p.mu.RLock()
	client, err := p.ownershipClientLocked()
	p.mu.RUnlock()
	if err != nil {
		return nil, err
	}

	tagFilter := tagtypes.TagFilter{Key: awssdk.String(ownershipTagKey)}
	if filter.Owner != "" {
		tagFilter.Values = []string{filter.Owner}
	}
	in := &resourcegroupstaggingapi.GetResourcesInput{TagFilters: []tagtypes.TagFilter{tagFilter}}

	var owners []interfaces.ResourceOwner
	for {
		resp, err := client.GetResources(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("aws: list owner tags: %w", err)
		}
		for _, mapping := range resp.ResourceTagMappingList {
			owner := ownerFromTags(mapping.Tags)
			if owner == "" {
				continue
			}
			ref := refFromOwnershipARN(awssdk.ToString(mapping.ResourceARN))
			if ref.ProviderID == "" {
				continue
			}
			if filter.ResourceType != "" && ref.Type != filter.ResourceType {
				continue
			}
			owners = append(owners, interfaces.ResourceOwner{Ref: ref, Owner: owner, Source: ownershipTagSource})
		}
		if awssdk.ToString(resp.PaginationToken) == "" {
			break
		}
		in.PaginationToken = resp.PaginationToken
	}
	return owners, nil
}

func (p *AWSProvider) ownershipClientLocked() (ownershipTaggingClient, error) {
	if !p.initialized {
		return nil, fmt.Errorf("aws: provider not initialized")
	}
	if p.ownershipClient == nil {
		return nil, fmt.Errorf("aws: ownership tagging client not initialized")
	}
	return p.ownershipClient, nil
}

func ownershipARN(ref interfaces.ResourceRef) (string, error) {
	if strings.HasPrefix(ref.ProviderID, "arn:") {
		return ref.ProviderID, nil
	}
	return "", fmt.Errorf("%w for %s/%s: got %q", ErrOwnershipARNRequired, ref.Type, ref.Name, ref.ProviderID)
}

func ownerFromTags(tags []tagtypes.Tag) string {
	for _, tag := range tags {
		if awssdk.ToString(tag.Key) == ownershipTagKey {
			return awssdk.ToString(tag.Value)
		}
	}
	return ""
}

func refFromOwnershipARN(arn string) interfaces.ResourceRef {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" {
		return interfaces.ResourceRef{}
	}
	resourceType := resourceTypeFromARN(parts[2], parts[5])
	if resourceType == "" {
		return interfaces.ResourceRef{}
	}
	return interfaces.ResourceRef{
		Name:       nameFromARNResource(parts[2], parts[5]),
		Type:       resourceType,
		ProviderID: arn,
	}
}

func resourceTypeFromARN(service, resource string) string {
	switch service {
	case "ecs":
		if strings.HasPrefix(resource, "service/") {
			return "infra.container_service"
		}
	case "eks":
		if strings.HasPrefix(resource, "cluster/") {
			return "infra.k8s_cluster"
		}
	case "rds":
		if strings.HasPrefix(resource, "db:") {
			return "infra.database"
		}
	case "elasticache":
		return "infra.cache"
	case "ec2":
		if strings.HasPrefix(resource, "vpc/") {
			return "infra.vpc"
		}
		if strings.HasPrefix(resource, "security-group/") {
			return "infra.firewall"
		}
	case "elasticloadbalancing":
		if strings.HasPrefix(resource, "loadbalancer/") {
			return "infra.load_balancer"
		}
	case "ecr":
		if strings.HasPrefix(resource, "repository/") {
			return "infra.registry"
		}
	case "apigateway":
		return "infra.api_gateway"
	case "iam":
		if strings.HasPrefix(resource, "role/") {
			return "infra.iam_role"
		}
	case "s3":
		return "infra.storage"
	case "acm":
		if strings.HasPrefix(resource, "certificate/") {
			return "infra.certificate"
		}
	case "application-autoscaling":
		if strings.HasPrefix(resource, "scalable-target/") {
			return "infra.autoscaling_group"
		}
	}
	return ""
}

func nameFromARNResource(service, resource string) string {
	switch service {
	case "ecs":
		parts := strings.Split(resource, "/")
		return parts[len(parts)-1]
	case "rds":
		return strings.TrimPrefix(resource, "db:")
	case "elasticloadbalancing":
		parts := strings.Split(resource, "/")
		if len(parts) >= 3 && parts[0] == "loadbalancer" {
			return parts[len(parts)-2]
		}
	case "application-autoscaling":
		return strings.TrimPrefix(resource, "scalable-target/")
	}
	parts := strings.FieldsFunc(resource, func(r rune) bool {
		return r == '/' || r == ':'
	})
	if len(parts) == 0 {
		return resource
	}
	return parts[len(parts)-1]
}

var _ interfaces.OwnershipProvider = (*AWSProvider)(nil)
