package provider_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/r2dts/terraform-provider-openshift/internal/provider"
)

// testProviderVersion is the version string used in unit/acceptance tests.
const testProviderVersion = "0.1.0"

// testAccProtoV6ProviderFactories is used in acceptance tests.
// It maps the provider address to a factory function returning a proto6 server.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"registry.terraform.io/r2dts/openshift": providerserver.NewProtocol6WithError(
		provider.New(testProviderVersion)(),
	),
}

// testAccKubeconfig returns a kubeconfig path for acceptance tests.
// It checks KUBECONFIG env first, then falls back to the CRC default location.
// Returns ("", false) if no kubeconfig is available.
func testAccKubeconfig() (string, bool) {
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		if _, err := os.Stat(kc); err == nil {
			return kc, true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	crcPath := filepath.Join(home, ".crc", "machines", "crc", "kubeconfig")
	if _, err := os.Stat(crcPath); err == nil {
		return crcPath, true
	}
	return "", false
}

// skipIfNoAcc skips the test if TF_ACC is not set.
func skipIfNoAcc(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Set TF_ACC=1 to run acceptance tests.")
	}
}

// skipIfNoKubeconfig skips the test if no usable kubeconfig is found.
func skipIfNoKubeconfig(t *testing.T) {
	t.Helper()
	if _, ok := testAccKubeconfig(); !ok {
		t.Skip(
			"Skipping: no kubeconfig available. " +
				"Set KUBECONFIG or start CRC so ~/.crc/machines/crc/kubeconfig exists.",
		)
	}
}
