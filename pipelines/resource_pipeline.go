package pipelines

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"

	"github.com/databrickslabs/terraform-provider-databricks/clusters"
	"github.com/databrickslabs/terraform-provider-databricks/common"
	"github.com/databrickslabs/terraform-provider-databricks/libraries"
)

// DefaultTimeout is the default amount of time that Terraform will wait when creating, updating and deleting pipelines.
const DefaultTimeout = 20 * time.Minute

// We separate this struct from Cluster for two reasons:
// 1. Pipeline clusters include a `Label` field.
// 2. Spark version is not required (and shouldn't be specified) for pipeline clusters.
// 3. num_workers is optional, and there is no single-node support for pipelines clusters.
type pipelineCluster struct {
	Label string `json:"label,omitempty"` // used only by pipelines

	NumWorkers int32               `json:"num_workers,omitempty" tf:"group:size"`
	Autoscale  *clusters.AutoScale `json:"autoscale,omitempty" tf:"group:size"`

	NodeTypeID           string                  `json:"node_type_id,omitempty" tf:"group:node_type,computed"`
	DriverNodeTypeID     string                  `json:"driver_node_type_id,omitempty" tf:"computed"`
	InstancePoolID       string                  `json:"instance_pool_id,omitempty" tf:"group:node_type"`
	DriverInstancePoolID string                  `json:"driver_instance_pool_id,omitempty"`
	AwsAttributes        *clusters.AwsAttributes `json:"aws_attributes,omitempty" tf:"suppress_diff"`
	GcpAttributes        *clusters.GcpAttributes `json:"gcp_attributes,omitempty" tf:"suppress_diff"`

	SparkConf    map[string]string `json:"spark_conf,omitempty"`
	SparkEnvVars map[string]string `json:"spark_env_vars,omitempty"`
	CustomTags   map[string]string `json:"custom_tags,omitempty"`

	SSHPublicKeys  []string                         `json:"ssh_public_keys,omitempty" tf:"max_items:10"`
	InitScripts    []clusters.InitScriptStorageInfo `json:"init_scripts,omitempty" tf:"max_items:10"` // TODO: tf:alias
	ClusterLogConf *clusters.StorageInfo            `json:"cluster_log_conf,omitempty"`
}

type notebookLibrary struct {
	Path string `json:"path"`
}

type pipelineLibrary struct {
	Jar      string           `json:"jar,omitempty"`
	Maven    *libraries.Maven `json:"maven,omitempty"`
	Whl      string           `json:"whl,omitempty"`
	Notebook *notebookLibrary `json:"notebook,omitempty"`
}

type filters struct {
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

type pipelineSpec struct {
	ID                  string            `json:"id,omitempty" tf:"computed"`
	Name                string            `json:"name,omitempty"`
	Storage             string            `json:"storage,omitempty" tf:"force_new"`
	Configuration       map[string]string `json:"configuration,omitempty"`
	Clusters            []pipelineCluster `json:"clusters,omitempty" tf:"slice_set,alias:cluster"`
	Libraries           []pipelineLibrary `json:"libraries,omitempty" tf:"slice_set,alias:library"`
	Filters             *filters          `json:"filters,omitempty"`
	Continuous          bool              `json:"continuous,omitempty"`
	Development         bool              `json:"development,omitempty"`
	AllowDuplicateNames bool              `json:"allow_duplicate_names,omitempty"`
	Target              string            `json:"target,omitempty"`
	Photon              bool              `json:"photon,omitempty"`
	Edition             string            `json:"edition,omitempty" tf:"suppress_diff,default:advanced"`
	Channel             string            `json:"channel,omitempty" tf:"suppress_diff,default:current"`
}

type createPipelineResponse struct {
	PipelineID string `json:"pipeline_id"`
}

// PipelineState ...
type PipelineState string

// Constants for PipelineStates
const (
	StateDeploying  PipelineState = "DEPLOYING"
	StateStarting   PipelineState = "STARTING"
	StateRunning    PipelineState = "RUNNING"
	StateStopping   PipelineState = "STOPPPING"
	StateDeleted    PipelineState = "DELETED"
	StateRecovering PipelineState = "RECOVERING"
	StateFailed     PipelineState = "FAILED"
	StateResetting  PipelineState = "RESETTING"
	StateIdle       PipelineState = "IDLE"
)

// PipelineHealthStatus ...
type PipelineHealthStatus string

// Constants for PipelineHealthStatus
const (
	HealthStatusHealthy   PipelineHealthStatus = "HEALTHY"
	HealthStatusUnhealthy PipelineHealthStatus = "UNHEALTHY"
)

type PipelineInfo struct {
	PipelineID      string                `json:"pipeline_id"`
	Spec            *pipelineSpec         `json:"spec"`
	State           *PipelineState        `json:"state"`
	Cause           string                `json:"cause"`
	ClusterID       string                `json:"cluster_id"`
	Name            string                `json:"name"`
	Health          *PipelineHealthStatus `json:"health"`
	CreatorUserName string                `json:"creator_user_name"`
}

type PipelinesAPI struct {
	client *common.DatabricksClient
	ctx    context.Context
}

func NewPipelinesAPI(ctx context.Context, m interface{}) PipelinesAPI {
	return PipelinesAPI{m.(*common.DatabricksClient), ctx}
}

func (a PipelinesAPI) Create(s pipelineSpec, timeout time.Duration) (string, error) {
	var resp createPipelineResponse
	err := a.client.Post(a.ctx, "/pipelines", s, &resp)
	if err != nil {
		return "", err
	}
	id := resp.PipelineID
	err = a.waitForState(id, timeout, StateRunning)
	if err != nil {
		log.Printf("[INFO] Pipeline creation failed, attempting to clean up pipeline %s", id)
		err2 := a.Delete(id, timeout)
		if err2 != nil {
			log.Printf("[WARN] Unable to delete pipeline %s; this resource needs to be manually cleaned up", id)
			return "", fmt.Errorf("multiple errors occurred when creating pipeline. Error while waiting for creation: \"%v\"; error while attempting to clean up failed pipeline: \"%v\"", err, err2)
		}
		log.Printf("[INFO] Successfully cleaned up pipeline %s", id)
		return "", err
	}
	return id, nil
}

func (a PipelinesAPI) Read(id string) (p PipelineInfo, err error) {
	err = a.client.Get(a.ctx, "/pipelines/"+id, nil, &p)
	return
}

func (a PipelinesAPI) Update(id string, s pipelineSpec, timeout time.Duration) error {
	err := a.client.Put(a.ctx, "/pipelines/"+id, s)
	if err != nil {
		return err
	}
	return a.waitForState(id, timeout, StateRunning)
}

func (a PipelinesAPI) Delete(id string, timeout time.Duration) error {
	err := a.client.Delete(a.ctx, "/pipelines/"+id, map[string]string{})
	if err != nil {
		return err
	}
	return resource.RetryContext(a.ctx, timeout,
		func() *resource.RetryError {
			i, err := a.Read(id)
			if err != nil {
				if common.IsMissing(err) {
					return nil
				}
				return resource.NonRetryableError(err)
			}
			message := fmt.Sprintf("Pipeline %s is in state %s, not yet deleted", id, *i.State)
			log.Printf("[DEBUG] %s", message)
			return resource.RetryableError(fmt.Errorf(message))
		})
}

func (a PipelinesAPI) waitForState(id string, timeout time.Duration, desiredState PipelineState) error {
	return resource.RetryContext(a.ctx, timeout,
		func() *resource.RetryError {
			i, err := a.Read(id)
			if err != nil {
				return resource.NonRetryableError(err)
			}
			state := *i.State
			if state == desiredState {
				return nil
			}
			if state == StateFailed {
				return resource.NonRetryableError(fmt.Errorf("pipeline %s has failed", id))
			}
			if !i.Spec.Continuous {
				// continuous pipelines just need a non-FAILED check
				return nil
			}
			message := fmt.Sprintf("Pipeline %s is in state %s, not yet in state %s", id, state, desiredState)
			log.Printf("[DEBUG] %s", message)
			return resource.RetryableError(fmt.Errorf(message))
		})
}

func adjustPipelineResourceSchema(m map[string]*schema.Schema) map[string]*schema.Schema {
	cluster, _ := m["cluster"].Elem.(*schema.Resource)
	clustersSchema := cluster.Schema
	clustersSchema["spark_conf"].DiffSuppressFunc = clusters.SparkConfDiffSuppressFunc
	common.MustSchemaPath(clustersSchema,
		"aws_attributes", "zone_id").DiffSuppressFunc = clusters.ZoneDiffSuppress

	awsAttributes, _ := clustersSchema["aws_attributes"].Elem.(*schema.Resource)
	awsAttributesSchema := awsAttributes.Schema
	delete(awsAttributesSchema, "availability")
	delete(awsAttributesSchema, "spot_bid_price_percent")
	delete(awsAttributesSchema, "ebs_volume_type")
	delete(awsAttributesSchema, "ebs_volume_count")
	delete(awsAttributesSchema, "ebs_volume_size")

	gcpAttributes, _ := clustersSchema["gcp_attributes"].Elem.(*schema.Resource)
	gcpAttributesSchema := gcpAttributes.Schema
	delete(gcpAttributesSchema, "use_preemptible_executors")
	delete(gcpAttributesSchema, "availability")
	delete(gcpAttributesSchema, "boot_disk_size")
	delete(gcpAttributesSchema, "zone_id")

	m["library"].MinItems = 1
	m["url"] = &schema.Schema{
		Type:     schema.TypeString,
		Computed: true,
	}
	m["channel"].ValidateFunc = validation.StringInSlice([]string{"current", "preview"}, true)
	m["edition"].ValidateFunc = validation.StringInSlice([]string{"pro", "core", "advanced"}, true)

	return m
}

// ResourcePipeline defines the Terraform resource for pipelines.
func ResourcePipeline() *schema.Resource {
	var pipelineSchema = common.StructToSchema(pipelineSpec{}, adjustPipelineResourceSchema)
	return common.Resource{
		Schema: pipelineSchema,
		Create: func(ctx context.Context, d *schema.ResourceData, c *common.DatabricksClient) error {
			var s pipelineSpec
			common.DataToStructPointer(d, pipelineSchema, &s)
			api := NewPipelinesAPI(ctx, c)
			id, err := api.Create(s, d.Timeout(schema.TimeoutCreate))
			if err != nil {
				return err
			}
			d.SetId(id)
			d.Set("url", c.FormatURL("#joblist/pipelines/", d.Id()))
			return nil
		},
		Read: func(ctx context.Context, d *schema.ResourceData, c *common.DatabricksClient) error {
			i, err := NewPipelinesAPI(ctx, c).Read(d.Id())
			if err != nil {
				return err
			}
			if i.Spec == nil {
				return fmt.Errorf("pipeline spec is nil for '%v'", i.PipelineID)
			}
			return common.StructToData(*i.Spec, pipelineSchema, d)
		},
		Update: func(ctx context.Context, d *schema.ResourceData, c *common.DatabricksClient) error {
			var s pipelineSpec
			common.DataToStructPointer(d, pipelineSchema, &s)
			return NewPipelinesAPI(ctx, c).Update(d.Id(), s, d.Timeout(schema.TimeoutUpdate))
		},
		Delete: func(ctx context.Context, d *schema.ResourceData, c *common.DatabricksClient) error {
			api := NewPipelinesAPI(ctx, c)
			return api.Delete(d.Id(), d.Timeout(schema.TimeoutDelete))
		},
		Timeouts: &schema.ResourceTimeout{
			Default: schema.DefaultTimeout(DefaultTimeout),
		},
	}.ToResource()
}
