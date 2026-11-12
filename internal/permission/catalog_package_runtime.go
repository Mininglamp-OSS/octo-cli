package permission

import "context"

// ConfigureCatalogPackage installs one already independently verified supported
// snapshot before any admission. The private startup registry owns support pins.
func (c *Coordinator) ConfigureCatalogPackage(v *VerifiedCatalog) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v == nil || len(c.scopes) != 0 || c.packageCatalog != nil || v.pin.Version != c.catalog.Version || v.pin.Digest != c.catalog.Digest || len(v.entries) != len(c.caps) {
		return ErrUnavailable
	}
	for id := range v.entries {
		if !c.caps[id] {
			return ErrUnavailable
		}
	}
	c.packageCatalog = v
	return nil
}

// CatalogPackage returns only this coordinator's exact immutable snapshot.
func (c *Coordinator) CatalogPackage() *VerifiedCatalog {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.packageCatalog
}

// CatalogSchema owns the actual cached schema read/materialization boundary.
func (c *Call) CatalogSchema(ctx context.Context, operation, facade string, config CatalogConfig) ([]byte, error) {
	var raw []byte
	err := c.check(ctx, func() error {
		v := c.coordinator.CatalogPackage()
		if v == nil {
			return PolicyUnavailable()
		}
		var err error
		raw, err = v.Schema(operation, facade, config)
		if err != nil {
			return PolicyUnavailable()
		}
		return err
	})
	return raw, err
}

// CatalogStatic owns a cached resource/inventory/module-map read before use.
func (c *Call) CatalogStatic(ctx context.Context, entry string, config CatalogConfig) ([]byte, error) {
	var raw []byte
	err := c.check(ctx, func() error {
		v := c.coordinator.CatalogPackage()
		if v == nil {
			return PolicyUnavailable()
		}
		var err error
		raw, err = v.Static(entry, config)
		if err != nil {
			return PolicyUnavailable()
		}
		return err
	})
	return raw, err
}

// CatalogDiscovery admits the selected immutable rows before matching/inference.
func (c *Call) CatalogDiscovery(ctx context.Context, service, facade string, config CatalogConfig) (rows [][]byte, matches []string, err error) {
	err = c.check(ctx, func() error {
		v := c.coordinator.CatalogPackage()
		if v == nil {
			return PolicyUnavailable()
		}
		var err error
		rows, matches, err = v.Discovery(service, facade, config)
		if err != nil {
			return PolicyUnavailable()
		}
		return err
	})
	return rows, matches, err
}
