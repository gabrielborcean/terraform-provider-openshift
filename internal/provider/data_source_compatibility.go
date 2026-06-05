package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &CompatibilityDataSource{}

// CompatibilityDataSource exposes the full CompatMatrix as a Terraform data source.
type CompatibilityDataSource struct{}

func NewCompatibilityDataSource() datasource.DataSource {
	return &CompatibilityDataSource{}
}

func (d *CompatibilityDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_compatibility"
}

// compatEntryAttrTypes describes the object type for a single matrix entry.
func compatEntryAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"ocp_version":      types.StringType,
		"min_provider_ver": types.StringType,
		"k8s_api_versions": types.ListType{ElemType: types.StringType},
		"min_oc_mirror":    types.StringType,
		"broken":           types.BoolType,
		"broken_reason":    types.StringType,
	}
}

type CompatibilityModel struct {
	Matrix types.List `tfsdk:"matrix"`
}

func (d *CompatibilityDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Returns the provider's OCP compatibility matrix.",
		Attributes: map[string]schema.Attribute{
			"matrix": schema.ListNestedAttribute{
				Computed:    true,
				Description: "List of OCP version compatibility entries.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"ocp_version":      schema.StringAttribute{Computed: true},
						"min_provider_ver": schema.StringAttribute{Computed: true},
						"k8s_api_versions": schema.ListAttribute{Computed: true, ElementType: types.StringType},
						"min_oc_mirror":    schema.StringAttribute{Computed: true},
						"broken":           schema.BoolAttribute{Computed: true},
						"broken_reason":    schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *CompatibilityDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	entryType := types.ObjectType{AttrTypes: compatEntryAttrTypes()}

	var entries []attr.Value
	for _, c := range CompatMatrix {
		k8sVals := make([]attr.Value, len(c.K8sAPIVersions))
		for i, v := range c.K8sAPIVersions {
			k8sVals[i] = types.StringValue(v)
		}
		k8sList, diags := types.ListValue(types.StringType, k8sVals)
		if diags.HasError() {
			resp.Diagnostics.Append(diags...)
			return
		}

		obj, diags := types.ObjectValue(compatEntryAttrTypes(), map[string]attr.Value{
			"ocp_version":      types.StringValue(c.OCPVersion),
			"min_provider_ver": types.StringValue(c.MinProviderVer),
			"k8s_api_versions": k8sList,
			"min_oc_mirror":    types.StringValue(c.MinOCMirror),
			"broken":           types.BoolValue(c.Broken),
			"broken_reason":    types.StringValue(c.BrokenReason),
		})
		if diags.HasError() {
			resp.Diagnostics.Append(diags...)
			return
		}
		entries = append(entries, obj)
	}

	matrix, diags := types.ListValue(entryType, entries)
	if diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	state := CompatibilityModel{Matrix: matrix}
	if diags := resp.State.Set(ctx, &state); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
}
