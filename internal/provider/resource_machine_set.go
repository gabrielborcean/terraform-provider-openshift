package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var machineSetGVR = k8sschema.GroupVersionResource{
	Group:    "machine.openshift.io",
	Version:  "v1beta1",
	Resource: "machinesets",
}

var _ resource.Resource = &MachineSetResource{}

type MachineSetResource struct {
	providerData *ProviderData
}

func NewMachineSetResource() resource.Resource {
	return &MachineSetResource{}
}

func (r *MachineSetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_machine_set"
}

type MachineSetModel struct {
	Name              types.String `tfsdk:"name"`
	Namespace         types.String `tfsdk:"namespace"`
	Replicas          types.Int64  `tfsdk:"replicas"`
	ClusterID         types.String `tfsdk:"cluster_id"`
	Role              types.String `tfsdk:"role"`
	ProviderSpec      types.String `tfsdk:"provider_spec"`
	Kubeconfig        types.String `tfsdk:"kubeconfig"`
	AvailableReplicas types.Int64  `tfsdk:"available_replicas"`
	ReadyReplicas     types.Int64  `tfsdk:"ready_replicas"`
	Phase             types.String `tfsdk:"phase"`
}

func (r *MachineSetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = tfschema.Schema{
		Description: "Manages a MachineSet on an OpenShift cluster (machine.openshift.io/v1beta1).",
		Attributes: map[string]tfschema.Attribute{
			"name": tfschema.StringAttribute{Required: true},
			"namespace": tfschema.StringAttribute{
				Optional: true, Computed: true,
				Default: stringdefault.StaticString("openshift-machine-api"),
			},
			"replicas":   tfschema.Int64Attribute{Required: true},
			"cluster_id": tfschema.StringAttribute{Required: true, Description: "Cluster infrastructure ID for label selectors."},
			"role":       tfschema.StringAttribute{Required: true, Description: "Node role: 'worker', 'infra', etc."},
			"provider_spec": tfschema.StringAttribute{
				Required:    true,
				Description: "JSON or YAML of the providerSpec (BareMetalMachineProviderSpec).",
			},
			"kubeconfig": tfschema.StringAttribute{
				Optional:    true,
				Description: "Path to kubeconfig. Overrides provider setting.",
			},
			"available_replicas": tfschema.Int64Attribute{Computed: true},
			"ready_replicas":     tfschema.Int64Attribute{Computed: true},
			"phase":              tfschema.StringAttribute{Computed: true},
		},
	}
}

func (r *MachineSetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected ProviderData", fmt.Sprintf("Expected *ProviderData, got %T", req.ProviderData))
		return
	}
	r.providerData = pd
}

func (r *MachineSetResource) getKubeClient(model *MachineSetModel) (dynamic.Interface, error) {
	kubeconfig := ""
	if !model.Kubeconfig.IsNull() && !model.Kubeconfig.IsUnknown() {
		kubeconfig = model.Kubeconfig.ValueString()
	} else if r.providerData != nil {
		kubeconfig = r.providerData.Kubeconfig
	}
	return buildKubeClient(kubeconfig)
}

func (r *MachineSetResource) buildObject(model *MachineSetModel) (*unstructured.Unstructured, error) {
	clusterID := model.ClusterID.ValueString()
	role := model.Role.ValueString()
	name := model.Name.ValueString()
	replicas := model.Replicas.ValueInt64()
	namespace := model.Namespace.ValueString()

	// Parse providerSpec — accept either JSON or YAML
	providerSpecStr := model.ProviderSpec.ValueString()
	var providerSpecObj map[string]interface{}
	if err := json.Unmarshal([]byte(providerSpecStr), &providerSpecObj); err != nil {
		// Try YAML
		if yamlErr := unmarshalYAML([]byte(providerSpecStr), &providerSpecObj); yamlErr != nil {
			return nil, fmt.Errorf("parsing provider_spec (tried JSON and YAML): JSON err: %v, YAML err: %v", err, yamlErr)
		}
	}

	machineLabels := map[string]interface{}{
		"machine.openshift.io/cluster-api-cluster":      clusterID,
		"machine.openshift.io/cluster-api-machine-role": role,
		"machine.openshift.io/cluster-api-machine-type": role,
		"machine.openshift.io/cluster-api-machineset":   name,
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "machine.openshift.io/v1beta1",
			"kind":       "MachineSet",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]interface{}{
					"machine.openshift.io/cluster-api-cluster": clusterID,
				},
			},
			"spec": map[string]interface{}{
				"replicas": replicas,
				"selector": map[string]interface{}{
					"matchLabels": machineLabels,
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": machineLabels,
					},
					"spec": map[string]interface{}{
						"providerSpec": map[string]interface{}{
							"value": providerSpecObj,
						},
					},
				},
			},
		},
	}

	return obj, nil
}

func (r *MachineSetResource) syncStatus(existing *unstructured.Unstructured, model *MachineSetModel) {
	if status, ok := existing.Object["status"].(map[string]interface{}); ok {
		if v, ok := status["availableReplicas"].(int64); ok {
			model.AvailableReplicas = types.Int64Value(v)
		} else if v, ok := status["availableReplicas"].(float64); ok {
			model.AvailableReplicas = types.Int64Value(int64(v))
		}
		if v, ok := status["readyReplicas"].(int64); ok {
			model.ReadyReplicas = types.Int64Value(v)
		} else if v, ok := status["readyReplicas"].(float64); ok {
			model.ReadyReplicas = types.Int64Value(int64(v))
		}
	}
	model.Phase = types.StringValue("Running")
}

func (r *MachineSetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MachineSetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	obj, err := r.buildObject(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Building MachineSet object", err.Error())
		return
	}

	result, err := client.Resource(machineSetGVR).Namespace(plan.Namespace.ValueString()).
		Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Creating MachineSet", err.Error())
		return
	}

	plan.AvailableReplicas = types.Int64Value(0)
	plan.ReadyReplicas = types.Int64Value(0)
	plan.Phase = types.StringValue("Provisioning")
	r.syncStatus(result, &plan)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MachineSetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MachineSetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&state)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	existing, err := client.Resource(machineSetGVR).Namespace(state.Namespace.ValueString()).
		Get(ctx, state.Name.ValueString(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading MachineSet", err.Error())
		return
	}

	r.syncStatus(existing, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MachineSetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MachineSetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	existing, err := client.Resource(machineSetGVR).Namespace(plan.Namespace.ValueString()).
		Get(ctx, plan.Name.ValueString(), metav1.GetOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Getting MachineSet for update", err.Error())
		return
	}

	obj, err := r.buildObject(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Building MachineSet object", err.Error())
		return
	}
	obj.SetResourceVersion(existing.GetResourceVersion())

	result, err := client.Resource(machineSetGVR).Namespace(plan.Namespace.ValueString()).
		Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Updating MachineSet", err.Error())
		return
	}

	r.syncStatus(result, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MachineSetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MachineSetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&state)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	err = client.Resource(machineSetGVR).Namespace(state.Namespace.ValueString()).
		Delete(ctx, state.Name.ValueString(), metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting MachineSet", err.Error())
	}
}
