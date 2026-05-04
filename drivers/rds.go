package drivers

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// RDSClient is the subset of RDS API used by RDSDriver.
type RDSClient interface {
	CreateDBInstance(ctx context.Context, params *rds.CreateDBInstanceInput, optFns ...func(*rds.Options)) (*rds.CreateDBInstanceOutput, error)
	DescribeDBInstances(ctx context.Context, params *rds.DescribeDBInstancesInput, optFns ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error)
	ModifyDBInstance(ctx context.Context, params *rds.ModifyDBInstanceInput, optFns ...func(*rds.Options)) (*rds.ModifyDBInstanceOutput, error)
	DeleteDBInstance(ctx context.Context, params *rds.DeleteDBInstanceInput, optFns ...func(*rds.Options)) (*rds.DeleteDBInstanceOutput, error)
}

// RDSDriver manages RDS database instances (infra.database).
type RDSDriver struct {
	client RDSClient
}

// NewRDSDriver creates an RDS driver from an AWS config.
func NewRDSDriver(cfg awssdk.Config) *RDSDriver {
	return &RDSDriver{client: rds.NewFromConfig(cfg)}
}

// NewRDSDriverWithClient creates an RDS driver with a custom client (for tests).
func NewRDSDriverWithClient(client RDSClient) *RDSDriver {
	return &RDSDriver{client: client}
}

func (d *RDSDriver) ResourceType() string { return "infra.database" }

func (d *RDSDriver) Create(ctx context.Context, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	engine, _ := spec.Config["engine"].(string)
	if engine == "" {
		engine = "postgres"
	}
	instanceClass, _ := spec.Config["instance_class"].(string)
	if instanceClass == "" {
		instanceClass = "db.t3.micro"
	}
	allocatedStorage := int32(intProp(spec.Config, "allocated_storage", 20))
	masterUser, _ := spec.Config["master_username"].(string)
	if masterUser == "" {
		masterUser = "admin"
	}
	masterPass, _ := spec.Config["master_password"].(string)
	if masterPass == "" {
		return nil, fmt.Errorf("rds: create %q: master_password is required", spec.Name)
	}
	multiAZ := boolProp(spec.Config, "multi_az", false)
	engineVersion, _ := spec.Config["engine_version"].(string)

	in := &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: awssdk.String(spec.Name),
		Engine:               awssdk.String(engine),
		DBInstanceClass:      awssdk.String(instanceClass),
		AllocatedStorage:     awssdk.Int32(allocatedStorage),
		MasterUsername:       awssdk.String(masterUser),
		MasterUserPassword:   awssdk.String(masterPass),
		MultiAZ:              awssdk.Bool(multiAZ),
	}
	if engineVersion != "" {
		in.EngineVersion = awssdk.String(engineVersion)
	}

	out, err := d.client.CreateDBInstance(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("rds: create %q: %w", spec.Name, err)
	}
	return rdsDBToOutput(out.DBInstance), nil
}

func (d *RDSDriver) Read(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.ResourceOutput, error) {
	out, err := d.client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: awssdk.String(ref.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("rds: describe %q: %w", ref.Name, err)
	}
	if len(out.DBInstances) == 0 {
		return nil, fmt.Errorf("rds: instance %q not found", ref.Name)
	}
	return rdsDBToOutput(&out.DBInstances[0]), nil
}

func (d *RDSDriver) Update(ctx context.Context, ref interfaces.ResourceRef, spec interfaces.ResourceSpec) (*interfaces.ResourceOutput, error) {
	in := &rds.ModifyDBInstanceInput{
		DBInstanceIdentifier: awssdk.String(ref.Name),
		ApplyImmediately:     awssdk.Bool(true),
	}
	if ic, _ := spec.Config["instance_class"].(string); ic != "" {
		in.DBInstanceClass = awssdk.String(ic)
	}
	if storage := intProp(spec.Config, "allocated_storage", 0); storage > 0 {
		in.AllocatedStorage = awssdk.Int32(int32(storage))
	}
	if multiAZ, ok := spec.Config["multi_az"].(bool); ok {
		in.MultiAZ = awssdk.Bool(multiAZ)
	}

	out, err := d.client.ModifyDBInstance(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("rds: modify %q: %w", ref.Name, err)
	}
	return rdsDBToOutput(out.DBInstance), nil
}

func (d *RDSDriver) Delete(ctx context.Context, ref interfaces.ResourceRef) error {
	_, err := d.client.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
		DBInstanceIdentifier: awssdk.String(ref.Name),
		SkipFinalSnapshot:    awssdk.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("rds: delete %q: %w", ref.Name, err)
	}
	return nil
}

func (d *RDSDriver) Diff(_ context.Context, desired interfaces.ResourceSpec, current *interfaces.ResourceOutput) (*interfaces.DiffResult, error) {
	if current == nil {
		return &interfaces.DiffResult{NeedsUpdate: true}, nil
	}
	changes := diffOutputs(desired.Config, current.Outputs)
	return &interfaces.DiffResult{NeedsUpdate: len(changes) > 0, Changes: changes}, nil
}

func (d *RDSDriver) HealthCheck(ctx context.Context, ref interfaces.ResourceRef) (*interfaces.HealthResult, error) {
	out, err := d.Read(ctx, ref)
	if err != nil {
		return &interfaces.HealthResult{Healthy: false, Message: err.Error()}, nil
	}
	status, _ := out.Outputs["db_status"].(string)
	healthy := status == "available"
	return &interfaces.HealthResult{Healthy: healthy, Message: status}, nil
}

func (d *RDSDriver) Scale(_ context.Context, _ interfaces.ResourceRef, _ int) (*interfaces.ResourceOutput, error) {
	return nil, fmt.Errorf("rds: use Update with instance_class to resize; replica count is managed via read replicas API")
}

func rdsDBToOutput(db *rdstypes.DBInstance) *interfaces.ResourceOutput {
	if db == nil {
		return nil
	}
	dbStatus := awssdk.ToString(db.DBInstanceStatus)
	status := "running"
	switch dbStatus {
	case "creating", "backing-up":
		status = "creating"
	case "deleting":
		status = "deleting"
	case "modifying":
		status = "updating"
	case "failed", "incompatible-parameters", "storage-full":
		status = "failed"
	}

	outputs := map[string]any{"db_status": dbStatus}
	if db.Engine != nil {
		outputs["engine"] = *db.Engine
	}
	if db.EngineVersion != nil {
		outputs["engine_version"] = *db.EngineVersion
	}
	if db.DBInstanceClass != nil {
		outputs["instance_class"] = *db.DBInstanceClass
	}
	if db.AllocatedStorage != nil {
		outputs["allocated_storage"] = int(*db.AllocatedStorage)
	}
	if db.MultiAZ != nil {
		outputs["multi_az"] = *db.MultiAZ
	}
	if db.DBInstanceArn != nil {
		outputs["arn"] = *db.DBInstanceArn
	}

	endpoint := ""
	if db.Endpoint != nil && db.Endpoint.Address != nil {
		endpoint = *db.Endpoint.Address
		if db.Endpoint.Port != nil {
			outputs["endpoint"] = fmt.Sprintf("%s:%d", endpoint, *db.Endpoint.Port)
		}
	}

	name := awssdk.ToString(db.DBInstanceIdentifier)
	return &interfaces.ResourceOutput{
		Name:       name,
		Type:       "infra.database",
		ProviderID: awssdk.ToString(db.DBInstanceArn),
		Outputs:    outputs,
		Status:     status,
	}
}

// SensitiveKeys returns output keys whose values should be masked in logs and plan output.
func (d *RDSDriver) SensitiveKeys() []string { return nil }

var _ interfaces.ResourceDriver = (*RDSDriver)(nil)
