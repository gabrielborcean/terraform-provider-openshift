package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var subscriptionGVR = k8sschema.GroupVersionResource{
	Group:    "operators.coreos.com",
	Version:  "v1alpha1",
	Resource: "subscriptions",
}

var _ resource.Resource = &SubscriptionResource{}

type SubscriptionResource struct {
	providerData *ProviderData
}

func NewSubscriptionResource() resource.Resource {
	return &SubscriptionResource{}
}

func (r *SubscriptionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subscription"
}

type SubscriptionConfigSpec struct {
	Env         types.List   `tfsdk:"env"`
	Tolerations types.String `tfsdk:"tolerations"`
	Resources   types.String `tfsdk:"resources"`
}

type SubscriptionModel struct {
	Name                types.String `tfsdk:"name"`
	Namespace           types.String `tfsdk:"namespace"`
	Source              types.String `tfsdk:"source"`
	SourceNamespace     types.String `tfsdk:"source_namespace"`
	PackageName         types.String `tfsdk:"package_name"`
	Channel             types.String `tfsdk:"channel"`
	InstallPlanApproval types.String `tfsdk:"install_plan_approval"`
	StartingCSV         types.String `tfsdk:"starting_csv"`
	Config              types.Object `tfsdk:"config"`
	Kubeconfig          types.String `tfsdk:"kubeconfig"`
	CurrentCSV          types.String `tfsdk:"current_csv"`
	InstalledCSV        types.String `tfsdk:"installed_csv"`
	State               types.String `tfsdk:"state"`
}

func (r *SubscriptionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = tfschema.Schema{
		Description: "Manages an OLM Subscription on an OpenShift cluster.",
		Attributes: map[string]tfschema.Attribute{
			"name":      tfschema.StringAttribute{Required: true},
			"namespace": tfschema.StringAttribute{Required: true},
			"source":    tfschema.StringAttribute{Required: true, Description: "CatalogSource name."},
			"source_namespace": tfschema.StringAttribute{
				Optional: true, Computed: true,
				Default: stringdefault.StaticString("openshift-marketplace"),
			},
			"package_name": tfschema.StringAttribute{Required: true},
			"channel":      tfschema.StringAttribute{Required: true},
			"install_plan_approval": tfschema.StringAttribute{
				Optional: true, Computed: true,
				Default:     stringdefault.StaticString("Automatic"),
				Description: "Install plan approval: 'Automatic' or 'Manual'.",
			},
			"starting_csv": tfschema.StringAttribute{Optional: true},
			"kubeconfig": tfschema.StringAttribute{
				Optional:    true,
				Description: "Path to kubeconfig. Overrides provider setting.",
			},
			"current_csv":   tfschema.StringAttribute{Computed: true},
			"installed_csv": tfschema.StringAttribute{Computed: true},
			"state":         tfschema.StringAttribute{Computed: true},
			"config": tfschema.SingleNestedAttribute{
				Optional:    true,
				Description: "Operator subscription configuration.",
				Attributes: map[string]tfschema.Attribute{
					"env": tfschema.ListAttribute{
						Optional:    true,
						ElementType: types.StringType,
						Description: "Environment variables as 'KEY=VALUE' strings.",
					},
					"tolerations": tfschema.StringAttribute{
						Optional:    true,
						Description: "Tolerations as JSON string.",
					},
					"resources": tfschema.StringAttribute{
						Optional:    true,
						Description: "Resource requirements as JSON string.",
					},
				},
			},
		},
	}
}

func (r *SubscriptionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *SubscriptionResource) getKubeClient(model *SubscriptionModel) (dynamic.Interface, error) {
	kubeconfig := ""
	if !model.Kubeconfig.IsNull() && !model.Kubeconfig.IsUnknown() {
		kubeconfig = model.Kubeconfig.ValueString()
	} else if r.providerData != nil {
		kubeconfig = r.providerData.Kubeconfig
	}
	return buildKubeClient(kubeconfig)
}

func (r *SubscriptionResource) buildObject(ctx context.Context, model *SubscriptionModel) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(k8sschema.GroupVersionKind{
		Group:   "operators.coreos.com",
		Version: "v1alpha1",
		Kind:    "Subscription",
	})
	obj.SetName(model.Name.ValueString())
	obj.SetNamespace(model.Namespace.ValueString())

	spec := map[string]interface{}{
		"channel":             model.Channel.ValueString(),
		"name":                model.PackageName.ValueString(),
		"source":              model.Source.ValueString(),
		"sourceNamespace":     model.SourceNamespace.ValueString(),
		"installPlanApproval": model.InstallPlanApproval.ValueString(),
	}

	if !model.StartingCSV.IsNull() && !model.StartingCSV.IsUnknown() && model.StartingCSV.ValueString() != "" {
		spec["startingCSV"] = model.StartingCSV.ValueString()
	}

	if !model.Config.IsNull() && !model.Config.IsUnknown() {
		var sc SubscriptionConfigSpec
		if diags := model.Config.As(ctx, &sc, basetypes.ObjectAsOptions{}); !diags.HasError() {
			configObj := map[string]interface{}{}
			if !sc.Env.IsNull() && !sc.Env.IsUnknown() {
				var envStrs []string
				sc.Env.ElementsAs(ctx, &envStrs, false)
				var envList []map[string]interface{}
				for _, e := range envStrs {
					parts := splitEnvVar(e)
					envList = append(envList, map[string]interface{}{
						"name":  parts[0],
						"value": parts[1],
					})
				}
				configObj["env"] = envList
			}
			if !sc.Tolerations.IsNull() && sc.Tolerations.ValueString() != "" {
				tolMap, err := jsonStringToMap(sc.Tolerations.ValueString())
				if err == nil {
					configObj["tolerations"] = tolMap
				}
			}
			if !sc.Resources.IsNull() && sc.Resources.ValueString() != "" {
				resMap, err := jsonStringToMap(sc.Resources.ValueString())
				if err == nil {
					configObj["resources"] = resMap
				}
			}
			spec["config"] = configObj
		}
	}

	obj.Object["spec"] = spec
	return obj
}

func splitEnvVar(s string) [2]string {
	for i, c := range s {
		if c == '=' {
			return [2]string{s[:i], s[i+1:]}
		}
	}
	return [2]string{s, ""}
}

func (r *SubscriptionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SubscriptionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	obj := r.buildObject(ctx, &plan)
	_, err = client.Resource(subscriptionGVR).Namespace(plan.Namespace.ValueString()).
		Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Creating Subscription", err.Error())
		return
	}

	// Set initial computed values
	plan.CurrentCSV = types.StringValue("")
	plan.InstalledCSV = types.StringValue("")
	plan.State = types.StringValue("Unknown")

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SubscriptionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SubscriptionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&state)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	existing, err := client.Resource(subscriptionGVR).Namespace(state.Namespace.ValueString()).
		Get(ctx, state.Name.ValueString(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading Subscription", err.Error())
		return
	}

	// Read status fields
	if status, ok := existing.Object["status"].(map[string]interface{}); ok {
		if csv, ok := status["currentCSV"].(string); ok {
			state.CurrentCSV = types.StringValue(csv)
		}
		if csv, ok := status["installedCSV"].(string); ok {
			state.InstalledCSV = types.StringValue(csv)
		}
		if st, ok := status["state"].(string); ok {
			state.State = types.StringValue(st)
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SubscriptionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SubscriptionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	existing, err := client.Resource(subscriptionGVR).Namespace(plan.Namespace.ValueString()).
		Get(ctx, plan.Name.ValueString(), metav1.GetOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Getting Subscription for update", err.Error())
		return
	}

	updated := r.buildObject(ctx, &plan)
	updated.SetResourceVersion(existing.GetResourceVersion())

	result, err := client.Resource(subscriptionGVR).Namespace(plan.Namespace.ValueString()).
		Update(ctx, updated, metav1.UpdateOptions{})
	if err != nil {
		resp.Diagnostics.AddError("Updating Subscription", err.Error())
		return
	}

	// Sync status
	if status, ok := result.Object["status"].(map[string]interface{}); ok {
		if csv, ok := status["currentCSV"].(string); ok {
			plan.CurrentCSV = types.StringValue(csv)
		}
		if csv, ok := status["installedCSV"].(string); ok {
			plan.InstalledCSV = types.StringValue(csv)
		}
		if st, ok := status["state"].(string); ok {
			plan.State = types.StringValue(st)
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SubscriptionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SubscriptionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := r.getKubeClient(&state)
	if err != nil {
		resp.Diagnostics.AddError("Building Kubernetes client", err.Error())
		return
	}

	err = client.Resource(subscriptionGVR).Namespace(state.Namespace.ValueString()).
		Delete(ctx, state.Name.ValueString(), metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting Subscription", err.Error())
	}
}
