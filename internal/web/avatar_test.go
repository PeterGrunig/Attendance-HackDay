package web

import "testing"

func TestSavedAvatarConfigDefaultsAndDropsUnavailableValues(t *testing.T) {
	config := &AvatarConfig{
		Base: "missing_base", HairStyle: "hat_star", Clothing: "cape_gold",
		Accessory: "missing_cosmetic", Effect: "trail_rainbow",
	}
	cfg := savedAvatarConfig(config, []string{"hat_star"})
	if cfg.Base != defaultAvatarBaseID {
		t.Fatalf("Base = %q, want %q", cfg.Base, defaultAvatarBaseID)
	}
	if cfg.HairStyle != "hat_star" {
		t.Fatalf("HairStyle = %q, want hat_star", cfg.HairStyle)
	}
	if cfg.Clothing != "" || cfg.Accessory != "" || cfg.Effect != "" {
		t.Fatalf("unavailable cosmetics were not dropped: %#v", cfg)
	}
}

func TestValidateAvatarConfigAcceptsOwnedCosmetics(t *testing.T) {
	owned := []string{"mike", "hat_star", "cape_gold", "glasses_rocket", "trail_rainbow"}
	cfg, err := validateAvatarConfig(owned, &AvatarConfig{
		Base: "mike", HairStyle: "hat_star", Clothing: "cape_gold",
		Accessory: "glasses_rocket", Effect: "trail_rainbow",
	})
	if err != nil {
		t.Fatalf("validateAvatarConfig returned error: %v", err)
	}
	if cfg.Base != "mike" || cfg.Effect != "trail_rainbow" {
		t.Fatalf("validated config did not preserve selections: %#v", cfg)
	}
}

func TestAvatarPreviewIncludesSelectedCosmeticLayers(t *testing.T) {
	preview := buildAvatarPreview(&AvatarConfig{
		Base: "gerald", HairStyle: "hat_star", Clothing: "cape_gold",
		Accessory: "glasses_rocket", Effect: "trail_rainbow",
	})
	if len(preview.Layers) != 4 {
		t.Fatalf("preview has %d layers, want 4", len(preview.Layers))
	}
	for _, layer := range preview.Layers {
		if layer.Image == "" {
			t.Fatalf("layer %s has empty image", layer.ID)
		}
	}
}

func TestGetShopItemViewsUsesSQLItemsAndOwnership(t *testing.T) {
	items := []ShopItem{
		{ID: "hat_star", Name: "Star Hat", Price: 5},
		{ID: "background_beach", Name: "Beach Background", Price: 15},
	}
	avatarItems, backgroundItems, owned := getShopItemViews(items, []string{"background_beach"})
	if len(avatarItems) != 1 || avatarItems[0].Image == "" {
		t.Fatalf("avatar items = %#v", avatarItems)
	}
	if len(backgroundItems) != 1 || backgroundItems[0].ThemeBackgroundID != "beach" {
		t.Fatalf("background items = %#v", backgroundItems)
	}
	if len(owned) != 1 || owned[0].ID != "background_beach" {
		t.Fatalf("owned items = %#v", owned)
	}
}

func TestOwnedThemeBackgroundOptionsOnlyIncludesPurchasedBackgrounds(t *testing.T) {
	options := ownedThemeBackgroundOptionViews([]string{"background_beach", "hat_star"})
	if len(options) != 1 || options[0].ID != "beach" || options[0].Label != "Beach" {
		t.Fatalf("owned background options = %#v", options)
	}
}
