package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var machineConfigGVR = k8sschema.GroupVersionResource{
	Group:    "machineconfiguration.openshift.io",
	Version:  "v1",
	Resource: "machineconfigs",
}

var _ resource.Resource = &MachineConfigResource{}

type MachineConfigResource struct {
	providerData *ProviderData
}

func NewMachineConfigResource() resource.Resource {
	return &MachineConfigResource{}
}

func (r *MachineConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_machine_config"
}

type MCFile struct {
	Path           types.String `tfsdk:"path"`
	Mode           types.Int64  `tfsdk:"mode"`
	ContentsSource types.String `tfsdk:"contents_source"`
	Overwrite      types.Bool   `tfsdk:"overwrite"`
}

type MCDropin struct {
	Name     types.String `tfsdk:"name"`
	Contents types.String `tfsdk:"contents"`
}

type MCUnit struct {
	Name     types.String `tfsdk:"name"`
	Enabled  types.Bool   `tfsdk:"enabled"`
	Contents types.String `tfsdk:"contents"`
	Dropins  types.List   `tfsdk:"dropins"`
}

type MachineConfigModel struct {
	Name            types.String `tfsdk:"name"`
	Labels          types.Map    `tfsdk:"labels"`
	KernelArguments types.List   `tfsdk:"kernel_arguments"`
	Extensions      types.List   `tfsdk:"extensions"`
	FIPS            types.Bool   `tfsdk:"fips"`
	KernelType      types.String `tfsdk:"kernel_type"`
	IgnitionVersion types.String `tfsdk:"ignition_version"`
	Files           types.List   `tfsdk:"files"`
	Units           types.List   `tfsdk:"units"`
	Kubeconfig      types.String `tfsdk:"kubeconfig"`
}

func dropinAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"name":     types.StringType,
		"contents": types.StringType,
	}
}

func mcUnitAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"name":     types.StringType,
		"enabled":  types.BoolType,
		"contents": types.StringType,
		"dropins":  types.ListType{ElemType: types.ObjectType{AttrTypes: dropinAttrTypes()}},
	}
}

func mcFileAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"path":            types.StringType,
		"mode":            types.Int64Type,
		"contents_source": types.StringType,
		"overwrite":       types.BoolType,
	}
}

// Ensure these helpers are referenced to avoid "declared and not used" errors.
var _ = mcUnitAttrTypes
var _ = mcFileAttrTypes

func (r *MachineConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = tfschema.Schema{
		Description: "Manages a MachineConfig object on an OpenShift cluster.",
		Attributes: map[string]tfschema.Attribute{
			"name": tfschema.StringAttribute{Required: true},
			"labels": tfschema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Labels to apply to the MachineConfig (must include machineconfiguration.openshift.io/role).",
			},
			"kernel_arguments": tfschema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
			},
			"extensions": tfschema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
			},
			"fips":        tfschema.BoolAttribute{Optional: true},
			"kernel_type": tfschema.StringAttribute{Optional: true},
			"ignition_version": tfschema.StringAttribute{
				Optional: true, Computed: true,
				Default: stringdefault.StaticString("3.2.0"),
			},
			"kubeconfig": tfschema.StringAttribute{
				Optional:    true,
				Description: "Path to kubeconfig. Overrides provider setting.",
			},
			"files": tfschema.ListNestedAttribute{
				Optional:    true,
				Description: "Files to place on nodes via Ignition.",
				NestedObject: tfschema.NestedAttributeObject{
					Attributes: map[string]tfschema.Attribute{
						"path": tfschema.StringAttribute{Required: true},
						"mode": tfschema.Int64Attribute{
							Optional: true, Computed: true,
							Default:     int64default.StaticInt64(0644),
							Description: "File permission mode (e.g. 0644 = 420).",
						},
						"contents_source": tfschema.StringAttribute{
							Required:    true,
							Description: "Ignition data URL (data:,<content> or data:text/plain;base64,<b64>).",
						},
						"overwrite": tfschema.BoolAttribute{
							Optional: true, Computed: true,
							Default: booldefault.StaticBool(true),
						},
					},
				},
			},
			"units": tfschema.ListNestedAttribute{
				Optional:    true,
				Description: "Systemd units to manage via Ignition.",
				NestedObject: tfschema.NestedAttributeObject{
					Attributes: map[string]tfschema.Attribute{
						"name":     tfschema.StringAttribute{Required: true},
						"enabled":  tfschema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
						"contents": tfschema.StringAttribute{Optional: true},
						"dropins": tfschema.ListNestedAttribute{
							Optional: true,
							NestedObject: tfschema.NestedAttributeObject{
								Attributes: map[string]tfschema.Attribute{
									"name":     tfschema.StringAttribute{Required: true},
									"contents": tfschema.StringAttribute{Required: true},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *MachineConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *MachineConfigResource) getKubeClient(model *MachineConfigModel) (dynamic.Interface, error) {
	kubeconfig := ""
	if !model.Kubeconfig.IsNull() && !model.Kubeconfig.IsUnknown() {
		kubeconfig = model.Kubeconfig.ValueString()
	} else if r.providerData != nil {
		kubeconfig = r.providerData.Kubeconfig
	}
	return buildKubeClient(kubeconfig)
}

func (r *MachineConfigResource) buildObject(ctx context.Context, model *MachineConfigModel) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(k8sschema.GroupVersionKind{
		Group:   "machineconfiguration.openshift.io",
		Version: "v1",
		Kind:    "MachineConfig",
	})
	obj.SetName(model.Name.ValueString())

	if !model.Labels.IsNull() && !model.Labels.IsUnknown() {
		var lbls map[string]string
		model.Labels.ElementsAs(ctx, &lbls, false)
		obj.SetLabels(lbls)
	}

	ignitionVersion := model.IgnitionVersion.ValueString()
	ignConfig := map[string]interface{}{
		"ignition": map[string]interface{}{
			"version": ignitionVersion,
		},
	}

	// files
	if !model.Files.IsNull() && !model.Files.IsUnknown() {
		var files []MCFile
		model.Files.ElementsAs(ctx, &files, false)
		var fileList []map[string]interface{}
		for _, f := range files {
			fm := map[string]interface{}{
				"path":      f.Path.ValueString(),
				"overwrite": f.Overwrite.ValueBool(),
				"contents": map[string]interface{}{
					"source": f.ContentsSource.ValueString(),
				},
			}
			if !f.Mode.IsNull() {
				fm["mode"] = int(f.Mode.ValueInt64())
			}
			fileList = append(fileList, fm)
		}
		ignConfig["storage"] = map[string]interface{}{"files": fileList}
	}

	// units
	if !model.Units.IsNull() && !model.Units.IsUnknown() {
		var units []MCUnit
		model.Units.ElementsAs(ctx, &units, false)
		var unitList []map[string]interface{}
		for _, u := range units {
			um := map[string]interface{}{
				"name":    u.Name.ValueString(),
				"enabled": u.Enabled.ValueBool(),
			}
			if !u.Contents.IsNull() && u.Contents.ValueString() != "" {
				um["contents"] = u.Contents.ValueString()
			}
			if !u.Dropins.IsNull() && !u.Dropins.IsUnknown() {
				var dropins []MCDropin
				u.Dropins.ElementsAs(ctx, &dropins, false)
				var dropinList []map[string]interface{}
				for _, d := range dropins {
					dropinList = append(dropinList, map[string]interface{}{
						"name":     d.Name.ValueString(),
						"contents": d.Contents.ValueString(),
					})
				}
				um["dropins"] = dropinList
			}
			unitList = append(unitList, um)
		}
		ignConfig["systemd"] = map[string]interface{}{"units": unitList}
	}

	spec := map[string]interface{}{
		"config": ignConfig,
	}

	if !model.KernelArguments.IsNull() && !model.KernelArguments.IsUnknown() {
		var kargs []string
		model.KernelArguments.ElementsAs(ctx, &kargs, false)
		spec["kernelArguments"] = kargs
	}

	if !model.Extensions.IsNull() && !model.Extensions.IsUnknown() {
		var exts []string
		model.Extensions.ElementsAs(ctx, &exts, false)
		spec["extensions"] = exts
	}

	if !model.FIPS.IsNull() && !model.FIPS.IsUnknown() {
		spec["fips"] = model.FIPS.ValueBool()
	}

	if !model.KernelType.IsNull() && !model.KernelType.IsUnknown() && model.KernelType.ValueString() != "" {
		spec["kernelType"] = model.KernelType.ValueString()
	}

	obj.Object["spec"] = spec
	return obj, nil
}

func (r *MachineConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MachineConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	obj, err := r.buildObject(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Building MachineConfig object", err.Error())
		return
	}

	_, err = client.Resource(machineConfigGVR).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Creating MachineConfig", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MachineConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MachineConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&state)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	_, err = client.Resource(machineConfigGVR).Get(ctx, state.Name.ValueString(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading MachineConfig", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MachineConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MachineConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	existing, err := client.Resource(machineConfigGVR).Get(ctx, plan.Name.ValueString(), metav1.GetOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Getting MachineConfig for update", err.Error())
		return
	}

	obj, err := r.buildObject(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Building MachineConfig object", err.Error())
		return
	}
	obj.SetResourceVersion(existing.GetResourceVersion())

	_, err = client.Resource(machineConfigGVR).Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Updating MachineConfig", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MachineConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MachineConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&state)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	err = client.Resource(machineConfigGVR).Delete(ctx, state.Name.ValueString(), metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting MachineConfig", err.Error())
	}
}
