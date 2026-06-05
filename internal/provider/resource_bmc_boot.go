package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ── registration ──────────────────────────────────────────────────────────────

var _ resource.Resource = &BMCBootResource{}

func NewBMCBootResource() resource.Resource { return &BMCBootResource{} }

type BMCBootResource struct{}

func (r *BMCBootResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bmc_boot"
}

// ── model ─────────────────────────────────────────────────────────────────────

type BMCBootModel struct {
	ISOURL      types.String `tfsdk:"iso_url"`
	Hosts       types.List   `tfsdk:"hosts"`
	BootedHosts types.List   `tfsdk:"booted_hosts"`
}

type BMCHostModel struct {
	Name        types.String `tfsdk:"name"`
	BMCAddress  types.String `tfsdk:"bmc_address"`
	BMCUsername types.String `tfsdk:"bmc_username"`
	BMCPassword types.String `tfsdk:"bmc_password"`
	Vendor      types.String `tfsdk:"vendor"`
}

var bmcHostAttrTypes = map[string]attr.Type{
	"name":         types.StringType,
	"bmc_address":  types.StringType,
	"bmc_username": types.StringType,
	"bmc_password": types.StringType,
	"vendor":       types.StringType,
}

// ── schema ────────────────────────────────────────────────────────────────────

func (r *BMCBootResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Mounts a discovery ISO via Redfish virtual media and reboots bare-metal hosts. " +
			"Use iso_url from openshift_cluster.discovery_iso_url. " +
			"Supports Dell iDRAC, HPE iLO, Supermicro, and any generic Redfish v1 BMC.",
		Attributes: map[string]schema.Attribute{
			"iso_url": schema.StringAttribute{
				Required:    true,
				Description: "URL of the discovery ISO to mount, e.g. openshift_cluster.prod.discovery_iso_url.",
			},
			"hosts": schema.ListNestedAttribute{
				Required:    true,
				Description: "List of bare-metal hosts to boot from the ISO.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Human-readable name for the host (used in error messages).",
						},
						"bmc_address": schema.StringAttribute{
							Required:    true,
							Description: "Base URL of the BMC Redfish endpoint, e.g. https://10.0.0.10 or https://idrac.example.internal.",
						},
						"bmc_username": schema.StringAttribute{
							Required:    true,
							Sensitive:   true,
							Description: "BMC username.",
						},
						"bmc_password": schema.StringAttribute{
							Required:    true,
							Sensitive:   true,
							Description: "BMC password.",
						},
						"vendor": schema.StringAttribute{
							Optional:    true,
							Computed:    true,
							Default:     stringdefault.StaticString("auto"),
							Description: "BMC vendor: auto (detect), dell, hpe, supermicro, generic. Defaults to auto.",
						},
					},
				},
			},
			"booted_hosts": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Names of hosts successfully booted from the ISO.",
			},
		},
	}
}

// ── CRUD ──────────────────────────────────────────────────────────────────────

func (r *BMCBootResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan BMCBootModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var hosts []BMCHostModel
	resp.Diagnostics.Append(plan.Hosts.ElementsAs(ctx, &hosts, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	isoURL := plan.ISOURL.ValueString()
	var booted []attr.Value

	for _, host := range hosts {
		client := newRedfishClient(host.BMCAddress.ValueString(), host.BMCUsername.ValueString(), host.BMCPassword.ValueString())
		vendor := host.Vendor.ValueString()
		if vendor == "auto" {
			vendor = client.detectVendor(ctx)
		}

		if err := client.mountAndBoot(ctx, isoURL, vendor); err != nil {
			resp.Diagnostics.AddError(
				fmt.Sprintf("Booting host %s (%s)", host.Name.ValueString(), host.BMCAddress.ValueString()),
				err.Error(),
			)
			// Continue to attempt remaining hosts before returning.
			continue
		}
		booted = append(booted, types.StringValue(host.Name.ValueString()))
	}

	bootedList, diags := types.ListValue(types.StringType, booted)
	resp.Diagnostics.Append(diags...)
	plan.BootedHosts = bootedList

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *BMCBootResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// Power state can change independently — treat as reconciled once booted.
	var state BMCBootModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BMCBootResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Re-boot with new ISO URL or host list — same as create.
	r.Create(ctx, resource.CreateRequest{Config: req.Config, Plan: req.Plan, ProviderMeta: req.ProviderMeta}, (*resource.CreateResponse)(resp))
}

func (r *BMCBootResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state BMCBootModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var hosts []BMCHostModel
	resp.Diagnostics.Append(state.Hosts.ElementsAs(ctx, &hosts, false)...)

	// Eject virtual media on all hosts (best-effort, don't fail destroy).
	for _, host := range hosts {
		client := newRedfishClient(host.BMCAddress.ValueString(), host.BMCUsername.ValueString(), host.BMCPassword.ValueString())
		vendor := host.Vendor.ValueString()
		if vendor == "auto" {
			vendor = client.detectVendor(ctx)
		}
		_ = client.ejectISO(ctx, vendor)
	}
}

// ── Redfish client ────────────────────────────────────────────────────────────

type redfishClient struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

func newRedfishClient(baseURL, username, password string) *redfishClient {
	return &redfishClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // BMCs use self-signed certs
			},
		},
	}
}

func (c *redfishClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("Redfish %s %s → %d: %s", method, path, resp.StatusCode, string(body))
	}
	return resp, nil
}

func (c *redfishClient) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// detectVendor reads the root Redfish endpoint and sniffs the vendor.
func (c *redfishClient) detectVendor(ctx context.Context) string {
	var root struct {
		Vendor string `json:"Vendor"`
		Oem    struct {
			Dell struct {
				ServiceTag string `json:"ServiceTag"`
			} `json:"Dell"`
			Hpe struct {
				Manager struct{} `json:"Manager"`
			} `json:"Hpe"`
		} `json:"Oem"`
	}
	if err := c.getJSON(ctx, "/redfish/v1/", &root); err != nil {
		return "generic"
	}
	v := strings.ToLower(root.Vendor)
	switch {
	case strings.Contains(v, "dell") || root.Oem.Dell.ServiceTag != "":
		return "dell"
	case strings.Contains(v, "hp") || strings.Contains(v, "hpe"):
		return "hpe"
	case strings.Contains(v, "supermicro"):
		return "supermicro"
	default:
		return "generic"
	}
}

// systemPath returns the Redfish Systems path for the vendor.
func systemPath(vendor string) string {
	switch vendor {
	case "dell":
		return "/redfish/v1/Systems/System.Embedded.1"
	default:
		return "/redfish/v1/Systems/1"
	}
}

// virtualMediaPath returns the virtual media CD path for the vendor.
func virtualMediaPath(vendor string) string {
	switch vendor {
	case "dell":
		return "/redfish/v1/Managers/iDRAC.Embedded.1/VirtualMedia/CD"
	case "hpe":
		return "/redfish/v1/Managers/1/VirtualMedia/2"
	default:
		return "/redfish/v1/Managers/1/VirtualMedia/CD"
	}
}

// mountAndBoot inserts the ISO as virtual media, sets boot override, and resets.
func (c *redfishClient) mountAndBoot(ctx context.Context, isoURL, vendor string) error {
	vmPath := virtualMediaPath(vendor)
	sysPath := systemPath(vendor)

	// 1. Eject any existing media.
	_ = c.ejectISO(ctx, vendor)

	// 2. Insert ISO.
	if err := c.insertISO(ctx, vmPath, isoURL, vendor); err != nil {
		return fmt.Errorf("inserting ISO: %w", err)
	}

	// 3. Set boot override to CD, once.
	_, err := c.do(ctx, http.MethodPatch, sysPath, map[string]any{
		"Boot": map[string]any{
			"BootSourceOverrideTarget":  "Cd",
			"BootSourceOverrideEnabled": "Once",
		},
	})
	if err != nil {
		return fmt.Errorf("setting boot override: %w", err)
	}

	// 4. Reset the system (power on if off, restart if on).
	powerState, _ := c.getPowerState(ctx, sysPath)
	resetType := "ForceRestart"
	if strings.EqualFold(powerState, "Off") {
		resetType = "On"
	}

	_, err = c.do(ctx, http.MethodPost, sysPath+"/Actions/ComputerSystem.Reset", map[string]any{
		"ResetType": resetType,
	})
	if err != nil {
		return fmt.Errorf("resetting system: %w", err)
	}

	return nil
}

func (c *redfishClient) insertISO(ctx context.Context, vmPath, isoURL, vendor string) error {
	// Try InsertMedia action first (newer Redfish), fall back to PATCH.
	actionPath := vmPath + "/Actions/VirtualMedia.InsertMedia"
	_, err := c.do(ctx, http.MethodPost, actionPath, map[string]any{
		"Image":                isoURL,
		"Inserted":             true,
		"WriteProtected":       true,
		"TransferProtocolType": transferProtocol(isoURL),
	})
	if err == nil {
		return nil
	}

	// Fall back to PATCH (older iDRAC, Supermicro).
	_, err = c.do(ctx, http.MethodPatch, vmPath, map[string]any{
		"Image":          isoURL,
		"Inserted":       true,
		"WriteProtected": true,
	})
	return err
}

func (c *redfishClient) ejectISO(ctx context.Context, vendor string) error {
	vmPath := virtualMediaPath(vendor)

	// Try EjectMedia action first.
	_, err := c.do(ctx, http.MethodPost, vmPath+"/Actions/VirtualMedia.EjectMedia", map[string]any{})
	if err == nil {
		return nil
	}

	// Fall back to PATCH.
	_, err = c.do(ctx, http.MethodPatch, vmPath, map[string]any{
		"Image":    "",
		"Inserted": false,
	})
	return err
}

func (c *redfishClient) getPowerState(ctx context.Context, sysPath string) (string, error) {
	var sys struct {
		PowerState string `json:"PowerState"`
	}
	if err := c.getJSON(ctx, sysPath, &sys); err != nil {
		return "", err
	}
	return sys.PowerState, nil
}

func transferProtocol(url string) string {
	switch {
	case strings.HasPrefix(url, "https://"):
		return "HTTPS"
	case strings.HasPrefix(url, "http://"):
		return "HTTP"
	case strings.HasPrefix(url, "nfs://"):
		return "NFS"
	default:
		return "HTTP"
	}
}
