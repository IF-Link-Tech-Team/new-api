package model

import (
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// StorefrontCatalogItem is one entry of the anonymous landing-page catalog.
// Preview marks display-only drafts (display_metadata.preview == true) that
// are not listed yet; purchase gates still require enabled+listed.
type StorefrontCatalogItem struct {
	Plan    SubscriptionPlan
	Preview bool
}

const storefrontCatalogTTL = time.Minute

var storefrontCatalogState = struct {
	sync.Mutex
	items    []StorefrontCatalogItem
	loadedAt time.Time
}{}

// storefrontPreviewFlag reads the display_metadata.preview marker. Malformed
// metadata never promotes a draft into the public catalog.
func storefrontPreviewFlag(displayMetadata string) bool {
	trimmed := strings.TrimSpace(displayMetadata)
	if trimmed == "" {
		return false
	}
	var metadata map[string]any
	if err := common.UnmarshalJsonStr(trimmed, &metadata); err != nil {
		return false
	}
	preview, ok := metadata["preview"].(bool)
	return ok && preview
}

// GetStorefrontCatalog returns listed plans plus preview drafts, cached in
// process for one minute (same pattern as GetPricing). Any plan write must
// go through InvalidateSubscriptionPlanCache, which also drops this cache.
func GetStorefrontCatalog() ([]StorefrontCatalogItem, error) {
	storefrontCatalogState.Lock()
	defer storefrontCatalogState.Unlock()
	if storefrontCatalogState.items != nil && time.Since(storefrontCatalogState.loadedAt) < storefrontCatalogTTL {
		return storefrontCatalogState.items, nil
	}
	var plans []SubscriptionPlan
	if err := DB.Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		return nil, err
	}
	items := make([]StorefrontCatalogItem, 0, len(plans))
	for i := range plans {
		plans[i].NormalizeDefaults()
		preview := storefrontPreviewFlag(plans[i].DisplayMetadata)
		if !plans[i].Listed && !preview {
			continue
		}
		items = append(items, StorefrontCatalogItem{Plan: plans[i], Preview: preview})
	}
	storefrontCatalogState.items = items
	storefrontCatalogState.loadedAt = time.Now()
	return items, nil
}

func invalidateStorefrontCatalogCache() {
	storefrontCatalogState.Lock()
	defer storefrontCatalogState.Unlock()
	storefrontCatalogState.items = nil
	storefrontCatalogState.loadedAt = time.Time{}
}
