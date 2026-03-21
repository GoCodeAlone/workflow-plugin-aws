package provider

import (
	"fmt"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// computeSizingMap maps abstract Size tiers to EC2 instance types (for compute/ECS/EKS nodes).
var computeSizingMap = map[interfaces.Size]string{
	interfaces.SizeXS: "t3.micro",
	interfaces.SizeS:  "t3.small",
	interfaces.SizeM:  "m5.large",
	interfaces.SizeL:  "m5.2xlarge",
	interfaces.SizeXL: "m5.4xlarge",
}

// rdsSizingMap maps abstract Size tiers to RDS instance classes.
var rdsSizingMap = map[interfaces.Size]string{
	interfaces.SizeXS: "db.t3.micro",
	interfaces.SizeS:  "db.t3.small",
	interfaces.SizeM:  "db.r6g.large",
	interfaces.SizeL:  "db.r6g.2xlarge",
	interfaces.SizeXL: "db.r6g.4xlarge",
}

// cacheSizingMap maps abstract Size tiers to ElastiCache node types.
var cacheSizingMap = map[interfaces.Size]string{
	interfaces.SizeXS: "cache.t3.micro",
	interfaces.SizeS:  "cache.t3.small",
	interfaces.SizeM:  "cache.m5.large",
	interfaces.SizeL:  "cache.r6g.2xlarge",
	interfaces.SizeXL: "cache.r6g.4xlarge",
}

func resolveSizing(resourceType string, size interfaces.Size, hints *interfaces.ResourceHints) (*interfaces.ProviderSizing, error) {
	switch resourceType {
	case "infra.database":
		instanceClass, ok := rdsSizingMap[size]
		if !ok {
			return nil, fmt.Errorf("aws: unsupported size %q for %s", size, resourceType)
		}
		specs := map[string]any{"instance_class": instanceClass}
		applyHints(specs, hints)
		return &interfaces.ProviderSizing{InstanceType: instanceClass, Specs: specs}, nil

	case "infra.cache":
		nodeType, ok := cacheSizingMap[size]
		if !ok {
			return nil, fmt.Errorf("aws: unsupported size %q for %s", size, resourceType)
		}
		specs := map[string]any{"node_type": nodeType}
		applyHints(specs, hints)
		return &interfaces.ProviderSizing{InstanceType: nodeType, Specs: specs}, nil

	default:
		instanceType, ok := computeSizingMap[size]
		if !ok {
			return nil, fmt.Errorf("aws: unsupported size %q for %s", size, resourceType)
		}
		specs := map[string]any{"instance_type": instanceType}
		applyHints(specs, hints)
		return &interfaces.ProviderSizing{InstanceType: instanceType, Specs: specs}, nil
	}
}

func applyHints(specs map[string]any, hints *interfaces.ResourceHints) {
	if hints == nil {
		return
	}
	if hints.CPU != "" {
		specs["cpu"] = hints.CPU
	}
	if hints.Memory != "" {
		specs["memory"] = hints.Memory
	}
	if hints.Storage != "" {
		specs["storage"] = hints.Storage
	}
}
