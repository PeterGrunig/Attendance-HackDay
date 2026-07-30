package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
)

const (
	defaultAvatarBaseID = "gerald"
	defaultAvatarImage  = "/static/images/avatars/gerald.png"
	avatarBasePrice     = 10

	// avatarCosmeticAssetVersion changes when cosmetic files are replaced in
	// place so browsers fetch the new shop and avatar artwork immediately.
	avatarCosmeticAssetVersion = "2"

	avatarSlotHairStyle = "hair_style"
	avatarSlotClothing  = "clothing"
	avatarSlotAccessory = "accessory"
	avatarSlotEffect    = "effect"
)

var (
	errInvalidAvatarSelection = errors.New("invalid avatar selection")
	errLockedAvatarSelection  = errors.New("locked avatar selection")
)

type avatarBaseOption struct {
	ID    string
	Label string
	Image string
}

type avatarCosmeticOption struct {
	ID    string
	Label string
	Slot  string
	Image string
}

var avatarBaseCatalog = []avatarBaseOption{
	{ID: "brainrot", Label: "BrainRot", Image: "/static/images/avatars/brainrot.png"},
	{ID: "d_money", Label: "D-Money", Image: "/static/images/avatars/d_money.png"},
	{ID: "funk_rapper", Label: "Funk Rapper", Image: "/static/images/avatars/funk_rapper.png"},
	{ID: "gerald", Label: "Gerald", Image: "/static/images/avatars/gerald.png"},
	{ID: "gopher", Label: "Gopher", Image: "/static/images/avatars/gopher.png"},
	{ID: "mike", Label: "Mike", Image: "/static/images/avatars/mike.png"},
	{ID: "milkman", Label: "Milk Man", Image: "/static/images/avatars/milkman.png"},
	{ID: "peter", Label: "Peter", Image: "/static/images/avatars/peter.png"},
	{ID: "salaryman", Label: "Salary Man", Image: "/static/images/avatars/salaryman.png"},
}

var avatarCosmeticCatalog = []avatarCosmeticOption{
	{ID: "hat_star", Label: "Star Hat", Slot: avatarSlotHairStyle, Image: avatarCosmeticImage("/static/images/cosmetics/hat_star.png")},
	{ID: "hat_wizard", Label: "Wizard Hat", Slot: avatarSlotHairStyle, Image: avatarCosmeticImage("/static/images/cosmetics/hat_wizard.png")},
	{ID: "crown_flower", Label: "Flower Crown", Slot: avatarSlotHairStyle, Image: avatarCosmeticImage("/static/images/cosmetics/crown_flower.png")},
	{ID: "cape_gold", Label: "Golden Cape", Slot: avatarSlotClothing, Image: avatarCosmeticImage("/static/images/cosmetics/cape_gold.png")},
	{ID: "hoodie_blue", Label: "Blue Hoodie", Slot: avatarSlotClothing, Image: avatarCosmeticImage("/static/images/cosmetics/hoodie_blue.png")},
	{ID: "scarf_red", Label: "Red Scarf", Slot: avatarSlotClothing, Image: avatarCosmeticImage("/static/images/cosmetics/scarf_red.png")},
	{ID: "glasses_rocket", Label: "Rocket Glasses", Slot: avatarSlotAccessory, Image: avatarCosmeticImage("/static/images/cosmetics/glasses_rocket.png")},
	{ID: "shades_pixel", Label: "Pixel Shades", Slot: avatarSlotAccessory, Image: avatarCosmeticImage("/static/images/cosmetics/shades_pixel.png")},
	{ID: "headphones_gem", Label: "Gem Headphones", Slot: avatarSlotAccessory, Image: avatarCosmeticImage("/static/images/cosmetics/headphones_gem.png")},
	{ID: "trail_rainbow", Label: "Rainbow Trail", Slot: avatarSlotEffect, Image: avatarCosmeticImage("/static/images/cosmetics/trail_rainbow.png")},
	{ID: "aura_sparkle", Label: "Sparkle Aura", Slot: avatarSlotEffect, Image: avatarCosmeticImage("/static/images/cosmetics/aura_sparkle.png")},
	{ID: "trail_comet", Label: "Comet Trail", Slot: avatarSlotEffect, Image: avatarCosmeticImage("/static/images/cosmetics/trail_comet.png")},
}

func avatarCosmeticImage(path string) string {
	return path + "?v=" + avatarCosmeticAssetVersion
}

// avatarView renders the saved avatar config, while preview and save POSTs reuse
// the same page data so the form always stays server-rendered.
func avatarView(w http.ResponseWriter, r *http.Request) {
	state, ok := currentStudentState(w, r, StudentStore.LoadStudentAvatarState)
	if !ok {
		return
	}
	data := buildAvatarPageData(state, savedAvatarConfig(state.AvatarConfig, state.OwnedShopItemIDs), r.URL.Query().Get("msg"), "")
	renderStudent(w, "avatarView.html", data)
}

func avatarPreviewView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := currentStudentState(w, r, StudentStore.LoadStudentAvatarState)
	if !ok {
		return
	}

	cfg, err := avatarConfigFromRequest(r, state.OwnedShopItemIDs)
	if err != nil {
		log.Printf("avatar preview rejected user_id=%q error=%v", state.User.UserID, err)
		data := buildAvatarPageData(state, savedAvatarConfig(state.AvatarConfig, state.OwnedShopItemIDs), "", avatarValidationMessage(err))
		renderStudent(w, "avatarView.html", data)
		return
	}

	data := buildAvatarPageData(state, cfg, "", "")
	data.ShowAvatarPreview = true
	renderStudent(w, "avatarView.html", data)
}

func avatarSaveView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, ok := currentStudentState(w, r, StudentStore.LoadStudentAvatarState)
	if !ok {
		return
	}

	cfg, err := avatarConfigFromRequest(r, state.OwnedShopItemIDs)
	if err != nil {
		log.Printf("avatar save rejected user_id=%q error=%v", state.User.UserID, err)
		data := buildAvatarPageData(state, savedAvatarConfig(state.AvatarConfig, state.OwnedShopItemIDs), "", avatarValidationMessage(err))
		renderStudent(w, "avatarView.html", data)
		return
	}

	if err := studentStore.SaveAvatarConfig(r.Context(), state.User.UserID, *cfg); err != nil {
		http.Error(w, "could not save avatar", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/avatar?msg="+url.QueryEscape("Avatar saved."), http.StatusSeeOther)
}

func savedAvatarConfig(config *AvatarConfig, ownedIDs []string) *AvatarConfig {
	return stripUnownedAvatarItems(ownedIDs, normalizeAvatarConfig(config))
}

func avatarConfigFromRequest(r *http.Request, ownedIDs []string) (*AvatarConfig, error) {
	if err := r.ParseForm(); err != nil {
		return nil, errInvalidAvatarSelection
	}

	cfg := &AvatarConfig{
		Base:      strings.TrimSpace(r.FormValue("base")),
		HairStyle: strings.TrimSpace(r.FormValue("hair_style")),
		Clothing:  strings.TrimSpace(r.FormValue("clothing")),
		Accessory: strings.TrimSpace(r.FormValue("accessory")),
		Effect:    strings.TrimSpace(r.FormValue("effect")),
	}

	return validateAvatarConfig(ownedIDs, cfg)
}

func validateAvatarConfig(ownedIDs []string, cfg *AvatarConfig) (*AvatarConfig, error) {
	if cfg == nil {
		return normalizeAvatarConfig(nil), nil
	}

	base := strings.TrimSpace(cfg.Base)
	if base == "" {
		base = defaultAvatarBaseID
	}
	if !avatarBaseExists(base) {
		return nil, errInvalidAvatarSelection
	}
	if !ownsAvatarBase(ownedIDs, base) {
		return nil, errLockedAvatarSelection
	}

	hairStyle := strings.TrimSpace(cfg.HairStyle)
	clothing := strings.TrimSpace(cfg.Clothing)
	accessory := strings.TrimSpace(cfg.Accessory)
	effect := strings.TrimSpace(cfg.Effect)

	if err := validateAvatarCosmetic(ownedIDs, hairStyle, avatarSlotHairStyle); err != nil {
		return nil, err
	}
	if err := validateAvatarCosmetic(ownedIDs, clothing, avatarSlotClothing); err != nil {
		return nil, err
	}
	if err := validateAvatarCosmetic(ownedIDs, accessory, avatarSlotAccessory); err != nil {
		return nil, err
	}
	if err := validateAvatarCosmetic(ownedIDs, effect, avatarSlotEffect); err != nil {
		return nil, err
	}

	return &AvatarConfig{
		Base:      base,
		HairStyle: hairStyle,
		Clothing:  clothing,
		Accessory: accessory,
		Effect:    effect,
	}, nil
}

func validateAvatarCosmetic(ownedIDs []string, itemID, slot string) error {
	if itemID == "" {
		return nil
	}

	option, ok := avatarCosmeticByID(itemID)
	if !ok || option.Slot != slot {
		return errInvalidAvatarSelection
	}
	if !ownsShopItem(ownedIDs, itemID) {
		return errLockedAvatarSelection
	}

	return nil
}

func normalizeAvatarConfig(cfg *AvatarConfig) *AvatarConfig {
	normalized := &AvatarConfig{
		Base: defaultAvatarBaseID,
	}

	if cfg == nil {
		return normalized
	}

	if avatarBaseExists(cfg.Base) {
		normalized.Base = cfg.Base
	}
	if avatarCosmeticExistsForSlot(cfg.HairStyle, avatarSlotHairStyle) {
		normalized.HairStyle = cfg.HairStyle
	}
	if avatarCosmeticExistsForSlot(cfg.Clothing, avatarSlotClothing) {
		normalized.Clothing = cfg.Clothing
	}
	if avatarCosmeticExistsForSlot(cfg.Accessory, avatarSlotAccessory) {
		normalized.Accessory = cfg.Accessory
	}
	if avatarCosmeticExistsForSlot(cfg.Effect, avatarSlotEffect) {
		normalized.Effect = cfg.Effect
	}

	return normalized
}

// stripUnownedAvatarItems keeps old or tampered saved selections from bypassing
// the same ownership boundary used by the avatar form.
func stripUnownedAvatarItems(ownedIDs []string, cfg *AvatarConfig) *AvatarConfig {
	if cfg == nil {
		return normalizeAvatarConfig(nil)
	}

	cleaned := *cfg
	if !ownsAvatarBase(ownedIDs, cleaned.Base) {
		cleaned.Base = defaultAvatarBaseID
	}
	if !ownsShopItem(ownedIDs, cleaned.HairStyle) {
		cleaned.HairStyle = ""
	}
	if !ownsShopItem(ownedIDs, cleaned.Clothing) {
		cleaned.Clothing = ""
	}
	if !ownsShopItem(ownedIDs, cleaned.Accessory) {
		cleaned.Accessory = ""
	}
	if !ownsShopItem(ownedIDs, cleaned.Effect) {
		cleaned.Effect = ""
	}

	return &cleaned
}

func getAvatarImage(config *AvatarConfig) string {
	base, ok := avatarBaseByID(normalizeAvatarConfig(config).Base)
	if !ok {
		return defaultAvatarImage
	}

	return base.Image
}

func buildAvatarPageData(state domain.StudentState, cfg *AvatarConfig, message, errorMessage string) PageData {
	normalized := normalizeAvatarConfig(cfg)
	preview := buildAvatarPreview(normalized)
	attendanceStatus, attendanceMessage, canMark := getTodayAttendanceState(state.Attendance, time.Now())

	return PageData{
		Title:                  "Avatar",
		Username:               state.User.Name,
		AvatarImage:            preview.BaseImage,
		AvatarSummary:          avatarSummary(normalized),
		Coins:                  state.CoinBalance,
		AttendanceStatus:       attendanceStatus,
		AttendanceMessage:      attendanceMessage,
		CanMarkAttendance:      canMark,
		ActiveNav:              "avatar",
		UseStudentCSS:          true,
		ThemeBackgroundOptions: ownedThemeBackgroundOptionViews(state.OwnedShopItemIDs),
		AvatarBaseOptions:      avatarBaseOptionViews(state.OwnedShopItemIDs, normalized.Base),
		AvatarHairOptions:      avatarCosmeticOptionViews(state.OwnedShopItemIDs, avatarSlotHairStyle, normalized.HairStyle),
		AvatarClothOptions:     avatarCosmeticOptionViews(state.OwnedShopItemIDs, avatarSlotClothing, normalized.Clothing),
		AvatarAccessOptions:    avatarCosmeticOptionViews(state.OwnedShopItemIDs, avatarSlotAccessory, normalized.Accessory),
		AvatarEffectOptions:    avatarCosmeticOptionViews(state.OwnedShopItemIDs, avatarSlotEffect, normalized.Effect),
		AvatarPreview:          preview,
		AvatarMessage:          message,
		AvatarError:            errorMessage,
	}
}

func buildAvatarPreview(cfg *AvatarConfig) AvatarPreviewView {
	normalized := normalizeAvatarConfig(cfg)
	base, ok := avatarBaseByID(normalized.Base)
	if !ok {
		base, _ = avatarBaseByID(defaultAvatarBaseID)
	}

	preview := AvatarPreviewView{
		BaseID:         base.ID,
		BaseLabel:      base.Label,
		BaseImage:      base.Image,
		HairStyleLabel: cosmeticLabel(normalized.HairStyle),
		ClothingLabel:  cosmeticLabel(normalized.Clothing),
		AccessoryLabel: cosmeticLabel(normalized.Accessory),
		EffectLabel:    cosmeticLabel(normalized.Effect),
		Layers: avatarLayerViews([]string{
			normalized.Effect,
			normalized.Clothing,
			normalized.HairStyle,
			normalized.Accessory,
		}),
	}
	preview.HasCosmetics = preview.HairStyleLabel != "" ||
		preview.ClothingLabel != "" ||
		preview.AccessoryLabel != "" ||
		preview.EffectLabel != ""

	return preview
}

func avatarBaseOptionViews(ownedIDs []string, selectedID string) []AvatarBaseOptionView {
	views := make([]AvatarBaseOptionView, 0, len(avatarBaseCatalog))
	for _, option := range avatarBaseCatalog {
		views = append(views, AvatarBaseOptionView{
			ID:       option.ID,
			Label:    option.Label,
			Image:    option.Image,
			Price:    avatarBasePrice,
			Owned:    ownsAvatarBase(ownedIDs, option.ID),
			Selected: option.ID == selectedID,
		})
	}
	return views
}

func ownsAvatarBase(ownedIDs []string, baseID string) bool {
	return baseID == defaultAvatarBaseID || ownsShopItem(ownedIDs, baseID)
}

func avatarCosmeticOptionViews(ownedIDs []string, slot, selectedID string) []AvatarCosmeticOptionView {
	views := []AvatarCosmeticOptionView{
		{
			Label:    "None",
			Slot:     slot,
			Owned:    true,
			Selected: selectedID == "",
		},
	}
	for _, option := range avatarCosmeticCatalog {
		if option.Slot != slot {
			continue
		}

		views = append(views, AvatarCosmeticOptionView{
			ID:       option.ID,
			Label:    option.Label,
			Slot:     option.Slot,
			Image:    option.Image,
			Owned:    ownsShopItem(ownedIDs, option.ID),
			Selected: option.ID == selectedID,
		})
	}

	sort.Slice(views[1:], func(i, j int) bool {
		return views[i+1].Label < views[j+1].Label
	})

	return views
}

func avatarSummary(cfg *AvatarConfig) []string {
	preview := buildAvatarPreview(cfg)
	summary := []string{preview.BaseLabel}
	for _, label := range []string{
		preview.HairStyleLabel,
		preview.ClothingLabel,
		preview.AccessoryLabel,
		preview.EffectLabel,
	} {
		if label != "" {
			summary = append(summary, label)
		}
	}
	return summary
}

func avatarBaseExists(id string) bool {
	_, ok := avatarBaseByID(id)
	return ok
}

func avatarBaseByID(id string) (avatarBaseOption, bool) {
	for _, option := range avatarBaseCatalog {
		if option.ID == id {
			return option, true
		}
	}
	return avatarBaseOption{}, false
}

func avatarCosmeticExistsForSlot(id, slot string) bool {
	if id == "" {
		return false
	}

	option, ok := avatarCosmeticByID(id)
	return ok && option.Slot == slot
}

func avatarCosmeticByID(id string) (avatarCosmeticOption, bool) {
	for _, option := range avatarCosmeticCatalog {
		if option.ID == id {
			return option, true
		}
	}
	return avatarCosmeticOption{}, false
}

func cosmeticLabel(id string) string {
	option, ok := avatarCosmeticByID(id)
	if !ok {
		return ""
	}
	return option.Label
}

func avatarLayerViews(ids []string) []AvatarLayerView {
	layers := make([]AvatarLayerView, 0, len(ids))
	for _, id := range ids {
		option, ok := avatarCosmeticByID(id)
		if !ok {
			continue
		}
		layers = append(layers, AvatarLayerView{
			ID:    option.ID,
			Label: option.Label,
			Slot:  option.Slot,
			Image: option.Image,
		})
	}
	return layers
}

func avatarValidationMessage(err error) string {
	switch {
	case errors.Is(err, errLockedAvatarSelection):
		return "You can only equip avatars and cosmetics you own."
	case errors.Is(err, errInvalidAvatarSelection):
		return "Choose a valid avatar option."
	default:
		return "Could not update avatar. Please try again."
	}
}
