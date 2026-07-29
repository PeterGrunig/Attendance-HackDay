package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	datastore "github.com/PeterGrunig/Attendance-HackDay/internal/store"
)

const attendanceRewardCoins = 1

func studentView(w http.ResponseWriter, r *http.Request) {
	state, ok := currentStudentState(w, r, StudentStore.LoadStudentDashboardState)
	if !ok {
		return
	}
	now := time.Now()
	status, message, canMark := getTodayAttendanceState(state.Attendance, now)
	avatar := savedAvatarConfig(state.AvatarConfig, state.OwnedShopItemIDs)
	data := PageData{
		Title: "Student Dashboard", Username: state.User.Name, AvatarImage: getAvatarImage(avatar),
		AvatarSummary: avatarSummary(avatar), AvatarPreview: buildAvatarPreview(avatar), Coins: state.CoinBalance,
		AttendanceStatus: status, AttendanceMessage: message, CanMarkAttendance: canMark,
		UpcomingDoubleDays: getUpcomingDoubleDays(state.Schedules),
		ActiveNav:          "home", UseStudentCSS: true,
		ThemeBackgroundOptions: ownedThemeBackgroundOptionViews(state.OwnedShopItemIDs),
	}
	renderStudent(w, "studentDash.html", data)
}

func attendanceView(w http.ResponseWriter, r *http.Request) {
	state, ok := currentStudentState(w, r, StudentStore.LoadStudentAttendanceState)
	if !ok {
		return
	}
	now := time.Now()
	reward := attendanceRewardCoins
	if isDoubleDay(state.Schedules, now) {
		reward *= 2
	}
	err := studentStore.MarkAttendanceAndAwardCoins(r.Context(), state.User.UserID, state.User.ClassroomID, now.Format("2006-01-02"), reward, now)
	if errors.Is(err, datastore.ErrAttendanceAlreadyMarked) {
		http.Redirect(w, r, "/studentDashboard", http.StatusSeeOther)
		return
	}
	if err != nil {
		http.Error(w, "could not mark attendance", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/studentDashboard", http.StatusSeeOther)
}

type studentStateLoader func(StudentStore, context.Context, domain.User) (domain.StudentState, error)

// currentStudentState loads the page-specific SQL state selected by its handler
// and logs internal failures without exposing database details in the response.
func currentStudentState(w http.ResponseWriter, r *http.Request, load studentStateLoader) (domain.StudentState, bool) {
	user, ok := authenticatedUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return domain.StudentState{}, false
	}
	if studentStore == nil {
		http.Error(w, "student store is not configured", http.StatusInternalServerError)
		return domain.StudentState{}, false
	}
	state, err := load(studentStore, r.Context(), user)
	if err != nil {
		log.Printf("load student state for %q: %v", user.UserID, err)
		http.Error(w, "could not load student data", http.StatusInternalServerError)
		return domain.StudentState{}, false
	}
	return state, true
}

func isDoubleDay(schedules []Schedule, now time.Time) bool {
	weekday := now.Weekday().String()
	for _, schedule := range schedules {
		if schedule.DoubleDay && schedule.DayOfWeek == weekday {
			return true
		}
	}
	return false
}

func getTodayAttendanceState(attendance AttendanceRecord, now time.Time) (status, message string, canMark bool) {
	today := now.Format("2006-01-02")
	for _, presentDate := range attendance.Present {
		if presentDate == today {
			return "Present today", "Attendance already marked for today.", false
		}
	}
	return "Not marked yet", "Tap Mark Attendance to earn coins.", true
}

func getUpcomingDoubleDays(schedules []Schedule) []DoubleDayView {
	items := make([]DoubleDayView, 0)
	for _, schedule := range schedules {
		if schedule.DoubleDay {
			items = append(items, DoubleDayView{DayOfWeek: schedule.DayOfWeek, StartTime: schedule.StartTime, EndTime: schedule.EndTime})
		}
	}
	sort.Slice(items, func(i, j int) bool { return weekdayIndex(items[i].DayOfWeek) < weekdayIndex(items[j].DayOfWeek) })
	return items
}

func weekdayIndex(day string) int {
	switch day {
	case "Sunday":
		return 0
	case "Monday":
		return 1
	case "Tuesday":
		return 2
	case "Wednesday":
		return 3
	case "Thursday":
		return 4
	case "Friday":
		return 5
	case "Saturday":
		return 6
	default:
		return 7
	}
}

func shopView(w http.ResponseWriter, r *http.Request) {
	state, ok := currentStudentState(w, r, StudentStore.LoadStudentShopState)
	if !ok {
		return
	}
	avatarItems, backgroundItems, ownedItems := getShopItemViews(state.ShopItems, state.OwnedShopItemIDs)
	lockedAvatars, cosmeticItems := splitAvatarShopItems(avatarItems)
	status, message, canMark := getTodayAttendanceState(state.Attendance, time.Now())
	avatar := savedAvatarConfig(state.AvatarConfig, state.OwnedShopItemIDs)
	data := PageData{
		Title: "Shop", Username: state.User.Name, AvatarImage: getAvatarImage(avatar), AvatarPreview: buildAvatarPreview(avatar), Coins: state.CoinBalance,
		AttendanceStatus: status, AttendanceMessage: message, CanMarkAttendance: canMark,
		LockedAvatarShopItems: lockedAvatars, AvatarShopItems: cosmeticItems,
		BackgroundShopItems: backgroundItems, OwnedShopItems: ownedItems,
		ShopMessage: r.URL.Query().Get("msg"), ActiveNav: "shop", UseStudentCSS: true,
		ThemeBackgroundOptions: ownedThemeBackgroundOptionViews(state.OwnedShopItemIDs),
	}
	renderStudent(w, "shopView.html", data)
}

func shopBuyView(w http.ResponseWriter, r *http.Request) {
	user, ok := authenticatedUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectShopMessage(w, r, "Invalid form submission.")
		return
	}
	err := studentStore.PurchaseShopItem(r.Context(), user.UserID, strings.TrimSpace(r.FormValue("item_id")), time.Now())
	switch {
	case errors.Is(err, datastore.ErrShopItemNotFound):
		redirectShopMessage(w, r, "That item does not exist.")
	case errors.Is(err, datastore.ErrShopItemAlreadyOwned):
		redirectShopMessage(w, r, "You already own that item.")
	case errors.Is(err, datastore.ErrInsufficientCoins):
		redirectShopMessage(w, r, "You do not have enough coins.")
	case err != nil:
		http.Error(w, "could not complete purchase", http.StatusInternalServerError)
	default:
		redirectShopMessage(w, r, "Purchase complete.")
	}
}

func redirectShopMessage(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/shop?msg="+url.QueryEscape(message), http.StatusSeeOther)
}

func getShopItemViews(items []ShopItem, ownedIDs []string) ([]ShopItemView, []ShopItemView, []ShopItemView) {
	avatarItems, backgroundItems, ownedItems := []ShopItemView{}, []ShopItemView{}, []ShopItemView{}
	for _, item := range items {
		view := ShopItemView{ID: item.ID, Name: item.Name, Description: item.Description, Price: item.Price, Owned: ownsShopItem(ownedIDs, item.ID)}
		if cosmetic, ok := avatarCosmeticByID(item.ID); ok {
			view.Image, view.Slot = cosmetic.Image, cosmetic.Slot
		} else if avatar, ok := avatarBaseByID(item.ID); ok {
			view.Image, view.Slot = avatar.Image, "base"
		} else if background, ok := themeBackgroundByShopItemID(item.ID); ok {
			view.Slot, view.ThemeBackgroundID = shopItemSlotTheme, background.ID
		}
		if view.Slot == shopItemSlotTheme {
			backgroundItems = append(backgroundItems, view)
		} else {
			avatarItems = append(avatarItems, view)
		}
		if view.Owned {
			ownedItems = append(ownedItems, view)
		}
	}

	// Keep purchasable characters ahead of cosmetics so locked base avatars
	// are visible without making students hunt through the full item catalog.
	sort.SliceStable(avatarItems, func(i, j int) bool {
		leftBase := avatarItems[i].Slot == "base"
		rightBase := avatarItems[j].Slot == "base"
		if leftBase != rightBase {
			return leftBase
		}
		return avatarItems[i].Name < avatarItems[j].Name
	})

	return avatarItems, backgroundItems, ownedItems
}

func splitAvatarShopItems(items []ShopItemView) ([]ShopItemView, []ShopItemView) {
	lockedAvatars, cosmetics := []ShopItemView{}, []ShopItemView{}
	for _, item := range items {
		if item.Slot == "base" {
			if !item.Owned {
				lockedAvatars = append(lockedAvatars, item)
			}
			continue
		}
		cosmetics = append(cosmetics, item)
	}
	return lockedAvatars, cosmetics
}

func ownsShopItem(ownedIDs []string, itemID string) bool {
	for _, ownedID := range ownedIDs {
		if ownedID == itemID {
			return true
		}
	}
	return false
}
