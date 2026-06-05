package provider

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var _ resource.Resource = &ImageMirrorResource{}

type ImageMirrorResource struct {
	providerData *ProviderData
}

func NewImageMirrorResource() resource.Resource {
	return &ImageMirrorResource{}
}

func (r *ImageMirrorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_image_mirror"
}

type OperatorPackage struct {
	Name       types.String `tfsdk:"name"`
	Channels   types.List   `tfsdk:"channels"`
	MinVersion types.String `tfsdk:"min_version"`
	MaxVersion types.String `tfsdk:"max_version"`
}

type OperatorCatalog struct {
	Catalog  types.String `tfsdk:"catalog"`
	Packages types.List   `tfsdk:"packages"`
}

type S3Backend struct {
	Bucket    types.String `tfsdk:"bucket"`
	Endpoint  types.String `tfsdk:"endpoint"`
	Region    types.String `tfsdk:"region"`
	AccessKey types.String `tfsdk:"access_key"`
	SecretKey types.String `tfsdk:"secret_key"`
}

type ICSOutput struct {
	Source  types.String `tfsdk:"source"`
	Mirrors types.List   `tfsdk:"mirrors"`
}

type ImageMirrorModel struct {
	RegistryURL        types.String `tfsdk:"registry_url"`
	OcBinary           types.String `tfsdk:"oc_binary"`
	PullSecretFile     types.String `tfsdk:"pull_secret_file"`
	ReleaseChannel     types.String `tfsdk:"release_channel"`
	ReleaseVersions    types.List   `tfsdk:"release_versions"`
	Operators          types.List   `tfsdk:"operators"`
	AdditionalImages   types.List   `tfsdk:"additional_images"`
	MirrorDir          types.String `tfsdk:"mirror_dir"`
	StorageBackendS3   types.Object `tfsdk:"storage_backend_s3"`
	SkipMissing        types.Bool   `tfsdk:"skip_missing"`
	DryRun             types.Bool   `tfsdk:"dry_run"`
	ImageContentSources types.List  `tfsdk:"image_content_sources"`
	MappingFile        types.String `tfsdk:"mapping_file"`
}

func s3AttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"bucket":     types.StringType,
		"endpoint":   types.StringType,
		"region":     types.StringType,
		"access_key": types.StringType,
		"secret_key": types.StringType,
	}
}

func icsOutputAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"source":  types.StringType,
		"mirrors": types.ListType{ElemType: types.StringType},
	}
}

func opPackageAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"name":        types.StringType,
		"channels":    types.ListType{ElemType: types.StringType},
		"min_version": types.StringType,
		"max_version": types.StringType,
	}
}

func operatorAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"catalog":  types.StringType,
		"packages": types.ListType{ElemType: types.ObjectType{AttrTypes: opPackageAttrTypes()}},
	}
}

func (r *ImageMirrorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Runs oc mirror to sync images to a disconnected registry.",
		Attributes: map[string]schema.Attribute{
			"registry_url": schema.StringAttribute{
				Required:    true,
				Description: "Target mirror registry URL (e.g. registry.example.com:5000).",
			},
			"oc_binary": schema.StringAttribute{
				Optional:    true,
				Description: "Path to oc binary. Overrides provider setting.",
			},
			"pull_secret_file": schema.StringAttribute{
				Required:    true,
				Description: "Path to pull-secret.json for source registry authentication.",
			},
			"release_channel": schema.StringAttribute{
				Optional:    true,
				Description: "OCP release channel (e.g. stable-4.14).",
			},
			"release_versions": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Specific OCP release versions to mirror.",
			},
			"additional_images": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Additional container images to mirror.",
			},
			"mirror_dir": schema.StringAttribute{
				Optional:    true,
				Description: "Local directory for disk-to-disk mirroring.",
			},
			"skip_missing": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Skip missing images instead of failing.",
			},
			"dry_run": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Perform a dry run without actually mirroring.",
			},
			"image_content_sources": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Parsed imageContentSources from oc mirror output.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"source": schema.StringAttribute{Computed: true},
						"mirrors": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
			"mapping_file": schema.StringAttribute{
				Computed:    true,
				Description: "Path to the generated mapping.txt file.",
			},
			"operators": schema.ListNestedAttribute{
				Optional:    true,
				Description: "Operator catalogs to mirror.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"catalog": schema.StringAttribute{Required: true},
						"packages": schema.ListNestedAttribute{
							Optional: true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"name":        schema.StringAttribute{Required: true},
									"channels":    schema.ListAttribute{Optional: true, ElementType: types.StringType},
									"min_version": schema.StringAttribute{Optional: true},
									"max_version": schema.StringAttribute{Optional: true},
								},
							},
						},
					},
				},
			},
			"storage_backend_s3": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "S3 storage backend for oc mirror.",
				Attributes: map[string]schema.Attribute{
					"bucket":     schema.StringAttribute{Required: true},
					"endpoint":   schema.StringAttribute{Required: true},
					"region":     schema.StringAttribute{Optional: true},
					"access_key": schema.StringAttribute{Optional: true},
					"secret_key": schema.StringAttribute{Optional: true, Sensitive: true},
				},
			},
		},
	}
}

func (r *ImageMirrorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ImageMirrorResource) resolveOcBinary(plan *ImageMirrorModel) string {
	if !plan.OcBinary.IsNull() && !plan.OcBinary.IsUnknown() && plan.OcBinary.ValueString() != "" {
		return plan.OcBinary.ValueString()
	}
	if r.providerData != nil && r.providerData.OcBinary != "" {
		return r.providerData.OcBinary
	}
	return "oc"
}

func (r *ImageMirrorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ImageMirrorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.runMirror(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Running oc mirror", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ImageMirrorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ImageMirrorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if the mapping file still exists
	if !state.MappingFile.IsNull() && state.MappingFile.ValueString() != "" {
		if _, err := os.Stat(state.MappingFile.ValueString()); os.IsNotExist(err) {
			resp.Diagnostics.AddWarning("Mapping file missing", "Mirror mapping file no longer exists; mirroring may need to be re-run.")
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ImageMirrorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ImageMirrorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.runMirror(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Running oc mirror", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ImageMirrorResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// Mirroring is additive; deletion is a no-op.
}

func (r *ImageMirrorResource) runMirror(ctx context.Context, plan *ImageMirrorModel) error {
	// Build ImageSetConfiguration YAML
	imageSetConfig, err := r.buildImageSetConfig(ctx, plan)
	if err != nil {
		return fmt.Errorf("building ImageSetConfiguration: %w", err)
	}

	// Write it to a temp file
	tmpDir, err := os.MkdirTemp("", "oc-mirror-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	imageSetConfigPath := filepath.Join(tmpDir, "imageset-config.yaml")
	if err := os.WriteFile(imageSetConfigPath, []byte(imageSetConfig), 0600); err != nil {
		return fmt.Errorf("writing ImageSetConfiguration: %w", err)
	}

	ocBinary := r.resolveOcBinary(plan)
	registryURL := "docker://" + plan.RegistryURL.ValueString()

	args := []string{
		"mirror",
		"--config", imageSetConfigPath,
		"--dest-skip-tls",
	}

	if !plan.SkipMissing.IsNull() && plan.SkipMissing.ValueBool() {
		args = append(args, "--skip-missing")
	}
	if !plan.DryRun.IsNull() && plan.DryRun.ValueBool() {
		args = append(args, "--dry-run")
	}

	var env []string
	if !plan.StorageBackendS3.IsNull() && !plan.StorageBackendS3.IsUnknown() {
		var s3 S3Backend
		if diags := plan.StorageBackendS3.As(ctx, &s3, basetypes.ObjectAsOptions{}); !diags.HasError() {
			if !s3.AccessKey.IsNull() {
				env = append(env, "AWS_ACCESS_KEY_ID="+s3.AccessKey.ValueString())
			}
			if !s3.SecretKey.IsNull() {
				env = append(env, "AWS_SECRET_ACCESS_KEY="+s3.SecretKey.ValueString())
			}
		}
	}

	if !plan.MirrorDir.IsNull() && !plan.MirrorDir.IsUnknown() && plan.MirrorDir.ValueString() != "" {
		// Disk-to-disk mode
		mirrorDir := plan.MirrorDir.ValueString()
		args = append(args, "file://"+mirrorDir)
	} else {
		args = append(args, registryURL)
	}

	stdout, stderr, err := runCommand(ctx, ocBinary, args, env, 4*time.Hour)
	if err != nil {
		return fmt.Errorf("oc mirror failed: %w\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	// Find mapping file and parse imageContentSources
	mappingFile := r.findMappingFile(plan.MirrorDir.ValueString())
	plan.MappingFile = types.StringValue(mappingFile)

	ics, err := r.parseImageContentSources(stdout + "\n" + stderr)
	if err != nil {
		// Non-fatal — set empty list
		emptyList, _ := types.ListValue(
			types.ObjectType{AttrTypes: icsOutputAttrTypes()},
			[]attr.Value{},
		)
		plan.ImageContentSources = emptyList
	} else {
		plan.ImageContentSources = ics
	}

	return nil
}

func (r *ImageMirrorResource) buildImageSetConfig(ctx context.Context, plan *ImageMirrorModel) (string, error) {
	type packageFilter struct {
		Name       string   `json:"name"`
		Channels   []string `json:"channels,omitempty"`
		MinVersion string   `json:"minVersion,omitempty"`
		MaxVersion string   `json:"maxVersion,omitempty"`
	}
	type operatorFilter struct {
		Catalog  string          `json:"catalog"`
		Packages []packageFilter `json:"packages,omitempty"`
	}
	type releaseFilter struct {
		Channels []map[string]interface{} `json:"channels,omitempty"`
	}
	type additionalImage struct {
		Name string `json:"name"`
	}
	type storageConfig struct {
		Local *map[string]string `json:"local,omitempty"`
		S3    *map[string]string `json:"s3,omitempty"`
	}
	type imageSetConfig struct {
		APIVersion     string          `json:"apiVersion"`
		Kind           string          `json:"kind"`
		StorageConfig  storageConfig   `json:"storageConfig"`
		Mirror         map[string]interface{} `json:"mirror"`
	}

	mirror := map[string]interface{}{}

	// Release images
	if !plan.ReleaseChannel.IsNull() && !plan.ReleaseChannel.IsUnknown() && plan.ReleaseChannel.ValueString() != "" {
		channels := []map[string]interface{}{
			{"name": plan.ReleaseChannel.ValueString()},
		}
		if !plan.ReleaseVersions.IsNull() && !plan.ReleaseVersions.IsUnknown() {
			var versions []string
			plan.ReleaseVersions.ElementsAs(ctx, &versions, false)
			if len(versions) > 0 {
				channels[0]["minVersion"] = versions[0]
				if len(versions) > 1 {
					channels[0]["maxVersion"] = versions[len(versions)-1]
				}
			}
		}
		mirror["platform"] = map[string]interface{}{
			"channels": channels,
			"graph":    true,
		}
	}

	// Operators
	if !plan.Operators.IsNull() && !plan.Operators.IsUnknown() {
		var ops []OperatorCatalog
		if diags := plan.Operators.ElementsAs(ctx, &ops, false); !diags.HasError() {
			var opFilters []operatorFilter
			for _, op := range ops {
				of := operatorFilter{Catalog: op.Catalog.ValueString()}
				if !op.Packages.IsNull() && !op.Packages.IsUnknown() {
					var pkgs []OperatorPackage
					op.Packages.ElementsAs(ctx, &pkgs, false)
					for _, pkg := range pkgs {
						pf := packageFilter{
							Name:       pkg.Name.ValueString(),
							MinVersion: pkg.MinVersion.ValueString(),
							MaxVersion: pkg.MaxVersion.ValueString(),
						}
						if !pkg.Channels.IsNull() {
							var chs []string
							pkg.Channels.ElementsAs(ctx, &chs, false)
							for _, ch := range chs {
								pf.Channels = append(pf.Channels, ch)
							}
						}
						of.Packages = append(of.Packages, pf)
					}
				}
				opFilters = append(opFilters, of)
			}
			mirror["operators"] = opFilters
		}
	}

	// Additional images
	if !plan.AdditionalImages.IsNull() && !plan.AdditionalImages.IsUnknown() {
		var imgs []string
		plan.AdditionalImages.ElementsAs(ctx, &imgs, false)
		var addlImgs []additionalImage
		for _, img := range imgs {
			addlImgs = append(addlImgs, additionalImage{Name: img})
		}
		mirror["additionalImages"] = addlImgs
	}

	// Storage config
	sc := storageConfig{}
	if !plan.StorageBackendS3.IsNull() && !plan.StorageBackendS3.IsUnknown() {
		var s3 S3Backend
		if diags := plan.StorageBackendS3.As(ctx, &s3, basetypes.ObjectAsOptions{}); !diags.HasError() {
			s3map := map[string]string{
				"bucket":         s3.Bucket.ValueString(),
				"endpoint":       s3.Endpoint.ValueString(),
				"region":         s3.Region.ValueString(),
				"accessKey":      s3.AccessKey.ValueString(),
				"secretAccessKey": s3.SecretKey.ValueString(),
			}
			sc.S3 = &s3map
		}
	} else if !plan.MirrorDir.IsNull() && plan.MirrorDir.ValueString() != "" {
		localMap := map[string]string{"path": plan.MirrorDir.ValueString()}
		sc.Local = &localMap
	} else {
		localMap := map[string]string{"path": "/tmp/oc-mirror-workspace"}
		sc.Local = &localMap
	}

	cfg := imageSetConfig{
		APIVersion:    "mirror.openshift.io/v1alpha2",
		Kind:          "ImageSetConfiguration",
		StorageConfig: sc,
		Mirror:        mirror,
	}

	data, err := marshalYAML(cfg)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (r *ImageMirrorResource) findMappingFile(mirrorDir string) string {
	if mirrorDir == "" {
		mirrorDir = "/tmp/oc-mirror-workspace"
	}
	// oc mirror places mapping.txt under the workspace
	candidate := filepath.Join(mirrorDir, "oc-mirror-workspace", "results-latest", "mapping.txt")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

func (r *ImageMirrorResource) parseImageContentSources(output string) (types.List, error) {
	// oc mirror prints imageContentSources YAML to stdout; parse it out.
	// We look for lines between "imageContentSources:" and the next blank line or end.
	type rawICS struct {
		Source  string   `json:"source"`
		Mirrors []string `json:"mirrors"`
	}

	var sources []rawICS
	inBlock := false
	var blockLines []string

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "imageContentSources:" {
			inBlock = true
			continue
		}
		if inBlock {
			if line == "" || (!strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "-")) {
				break
			}
			blockLines = append(blockLines, line)
		}
	}

	if len(blockLines) > 0 {
		yamlStr := strings.Join(blockLines, "\n")
		var rawList []rawICS
		if err := unmarshalYAML([]byte(yamlStr), &rawList); err == nil {
			sources = rawList
		}
	}

	elemType := types.ObjectType{AttrTypes: icsOutputAttrTypes()}
	var elems []attr.Value
	for _, src := range sources {
		mirrorVals := make([]attr.Value, len(src.Mirrors))
		for i, m := range src.Mirrors {
			mirrorVals[i] = types.StringValue(m)
		}
		mirrorList, _ := types.ListValue(types.StringType, mirrorVals)
		obj, _ := types.ObjectValue(icsOutputAttrTypes(), map[string]attr.Value{
			"source":  types.StringValue(src.Source),
			"mirrors": mirrorList,
		})
		elems = append(elems, obj)
	}

	list, _ := types.ListValue(elemType, elems)
	return list, nil
}
