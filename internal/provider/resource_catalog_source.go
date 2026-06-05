package provider

import (
	"context"
	"fmt"
	"time"

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

var catalogSourceGVR = k8sschema.GroupVersionResource{
	Group:    "operators.coreos.com",
	Version:  "v1alpha1",
	Resource: "catalogsources",
}

var _ resource.Resource = &CatalogSourceResource{}

type CatalogSourceResource struct {
	providerData *ProviderData
}

func NewCatalogSourceResource() resource.Resource {
	return &CatalogSourceResource{}
}

func (r *CatalogSourceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_catalog_source"
}

type CatalogSourceModel struct {
	Name           types.String `tfsdk:"name"`
	Namespace      types.String `tfsdk:"namespace"`
	DisplayName    types.String `tfsdk:"display_name"`
	Image          types.String `tfsdk:"image"`
	Publisher      types.String `tfsdk:"publisher"`
	UpdateStrategy types.String `tfsdk:"update_strategy"`
	PollInterval   types.String `tfsdk:"poll_interval"`
	Kubeconfig     types.String `tfsdk:"kubeconfig"`
}

func (r *CatalogSourceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = tfschema.Schema{
		Description: "Manages an OperatorHub CatalogSource on an OpenShift cluster.",
		Attributes: map[string]tfschema.Attribute{
			"name": tfschema.StringAttribute{Required: true},
			"namespace": tfschema.StringAttribute{
				Optional: true, Computed: true,
				Default: stringdefault.StaticString("openshift-marketplace"),
			},
			"display_name": tfschema.StringAttribute{Optional: true},
			"image":        tfschema.StringAttribute{Required: true},
			"publisher": tfschema.StringAttribute{
				Optional: true, Computed: true,
				Default: stringdefault.StaticString("Custom"),
			},
			"update_strategy": tfschema.StringAttribute{
				Optional: true, Computed: true,
				Default:     stringdefault.StaticString("RegistryPoll"),
				Description: "Update strategy: 'RegistryPoll' or 'None'.",
			},
			"poll_interval": tfschema.StringAttribute{
				Optional: true, Computed: true,
				Default:     stringdefault.StaticString("30m"),
				Description: "Poll interval for RegistryPoll update strategy (e.g. 30m).",
			},
			"kubeconfig": tfschema.StringAttribute{
				Optional:    true,
				Description: "Path to kubeconfig. Overrides provider setting.",
			},
		},
	}
}

func (r *CatalogSourceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *CatalogSourceResource) getKubeClient(model *CatalogSourceModel) (dynamic.Interface, error) {
	kubeconfig := ""
	if !model.Kubeconfig.IsNull() && !model.Kubeconfig.IsUnknown() {
		kubeconfig = model.Kubeconfig.ValueString()
	} else if r.providerData != nil {
		kubeconfig = r.providerData.Kubeconfig
	}
	return buildKubeClient(kubeconfig)
}

func (r *CatalogSourceResource) buildObject(model *CatalogSourceModel) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(k8sschema.GroupVersionKind{
		Group:   "operators.coreos.com",
		Version: "v1alpha1",
		Kind:    "CatalogSource",
	})
	obj.SetName(model.Name.ValueString())
	obj.SetNamespace(model.Namespace.ValueString())

	spec := map[string]interface{}{
		"sourceType":  "grpc",
		"image":       model.Image.ValueString(),
		"displayName": model.DisplayName.ValueString(),
		"publisher":   model.Publisher.ValueString(),
	}

	updateStrategy := model.UpdateStrategy.ValueString()
	if updateStrategy == "RegistryPoll" {
		pollInterval := model.PollInterval.ValueString()
		// Convert human-readable to ISO 8601 duration if needed
		duration, err := time.ParseDuration(pollInterval)
		if err == nil {
			mins := int(duration.Minutes())
			pollInterval = fmt.Sprintf("PT%dM", mins)
		}
		spec["updateStrategy"] = map[string]interface{}{
			"registryPoll": map[string]interface{}{
				"interval": pollInterval,
			},
		}
	}

	obj.Object["spec"] = spec
	return obj
}

func (r *CatalogSourceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan CatalogSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	obj := r.buildObject(&plan)
	_, err = client.Resource(catalogSourceGVR).Namespace(plan.Namespace.ValueString()).
		Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Creating CatalogSource", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CatalogSourceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state CatalogSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&state)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	existing, err := client.Resource(catalogSourceGVR).Namespace(state.Namespace.ValueString()).
		Get(ctx, state.Name.ValueString(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading CatalogSource", err.Error())
		return
	}

	// Sync back computed fields from the live object
	if spec, ok := existing.Object["spec"].(map[string]interface{}); ok {
		if img, ok := spec["image"].(string); ok {
			state.Image = types.StringValue(img)
		}
		if pub, ok := spec["publisher"].(string); ok {
			state.Publisher = types.StringValue(pub)
		}
		if dn, ok := spec["displayName"].(string); ok {
			state.DisplayName = types.StringValue(dn)
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *CatalogSourceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan CatalogSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	existing, err := client.Resource(catalogSourceGVR).Namespace(plan.Namespace.ValueString()).
		Get(ctx, plan.Name.ValueString(), metav1.GetOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Getting CatalogSource for update", err.Error())
		return
	}

	updated := r.buildObject(&plan)
	updated.SetResourceVersion(existing.GetResourceVersion())

	_, err = client.Resource(catalogSourceGVR).Namespace(plan.Namespace.ValueString()).
		Update(ctx, updated, metav1.UpdateOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Updating CatalogSource", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CatalogSourceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state CatalogSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&state)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	err = client.Resource(catalogSourceGVR).Namespace(state.Namespace.ValueString()).
		Delete(ctx, state.Name.ValueString(), metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting CatalogSource", err.Error())
	}
}
