package config

import (
	"fmt"
	"net"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/buildpolicy"
)

func validateConservative(c Config) error {
	if !buildpolicy.Conservative {
		return nil
	}
	for _, address := range []string{c.ListenAddr, c.PreviewListenAddr} {
		host, _, err := net.SplitHostPort(address)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return fmt.Errorf("conservative trial requires literal loopback listeners")
		}
	}
	if !c.AdminAuthEnabled || strings.TrimSpace(c.AdminPassword) == "" {
		return fmt.Errorf("conservative trial requires separate management authentication")
	}
	if c.WAABackend != WAABackendCamoufox || c.FirstEventHedge != FirstEventHedgeOff || c.PerAccountConcurrency != 1 || c.MaxActiveWorkers != 1 || c.WarmStartupConcurrency != 1 || c.WarmWorkerLimit > 1 {
		return fmt.Errorf("conservative trial requires one browser worker, one concurrent request and no hedging")
	}
	if c.RequestBodyLog || c.FakeStream || c.StripThoughtSignatures || c.ContinuationRewrite || len(c.UpstreamChannels) != 1 || c.UpstreamChannels[0] != "playground" {
		return fmt.Errorf("conservative trial requires native Playground responses and disabled body logging")
	}
	return nil
}

// ValidateRuntimePolicy covers the credential-independent Config structure and
// the secret that must be present before the local service can start or persist.
func ValidateRuntimePolicy(c Config) error {
	if err := validateConservative(c); err != nil {
		return err
	}
	if buildpolicy.Conservative && strings.TrimSpace(c.ProxyAPIKey) == "" {
		return fmt.Errorf("conservative trial requires a local API key")
	}
	return nil
}
