package externaljwt

import (
	"context"
	"sync"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
)

// manager is the process-wide registry of configured providers. It is
// populated once at gateway startup by Init and read thereafter by the auth
// interceptors via Get.
type manager struct {
	mu        sync.RWMutex
	providers map[IssuerType]Provider
}

var mgr = &manager{providers: map[IssuerType]Provider{}}

// Init reads configuration from appconfig and initializes any configured
// providers. It is safe to call multiple times but subsequent calls are
// no-ops. Init returns an error if a provider is configured but its
// initial bundle fetch fails; callers should log and continue rather
// than crashing, because the provider is still registered and its
// background refresh loop will retry on its own. Crashing here would
// take unrelated auth paths (DSN, static tokens) offline as collateral
// damage for a SPIFFE-only hiccup.
//
// The background refresh goroutines are started by Init and only stopped
// when Close is called on the manager.
func Init(ctx context.Context) error {
	cfg := appconfig.Get()
	if !cfg.SPIFFEEnabled() {
		log.Debug("spiffe: disabled")
		return nil
	}

	p, err := NewSPIFFEProvider(ctx, SPIFFEConfig{
		TrustDomain:   cfg.SPIFFETrustDomain(),
		Audience:      cfg.SPIFFEAudience(),
		BundleURL:     cfg.SPIFFEBundleURL(),
		BundleFile:    cfg.SPIFFEBundleFile(),
		RefreshPeriod: cfg.SPIFFERefreshPeriod(),
	})
	if err != nil {
		// p may still be non-nil if only the initial refresh failed;
		// register it so retries happen in the background but surface
		// the error to the caller.
		if p != nil {
			mgr.register(p)
			go p.Start(context.Background())
		}
		return err
	}
	mgr.register(p)
	go p.Start(context.Background())
	log.With(
		"trust_domain", cfg.SPIFFETrustDomain(),
		"audience", cfg.SPIFFEAudience(),
	).Info("spiffe: provider initialized")
	return nil
}

func (m *manager) register(p Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[p.Type()] = p
}

// Get returns the provider registered for the given issuer type, or
// ErrNotConfigured if none is configured. Callers should treat
// ErrNotConfigured as "skip external-JWT validation" rather than as an
// authentication failure.
func Get(t IssuerType) (Provider, error) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	p, ok := mgr.providers[t]
	if !ok {
		return nil, ErrNotConfigured
	}
	return p, nil
}

// Close releases all registered providers. Intended for tests and graceful
// shutdown.
func Close() {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	for _, p := range mgr.providers {
		_ = p.Close()
	}
	mgr.providers = map[IssuerType]Provider{}
}
