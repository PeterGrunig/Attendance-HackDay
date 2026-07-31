package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
	datastore "github.com/PeterGrunig/Attendance-HackDay/internal/store"
)

type studentPrizePageData struct {
	PageData
	Catalogs    []domain.PrizeCatalog
	Redemptions []domain.PrizeRedemption
	CSRFToken   string
	Message     string
	Error       string
}

type teacherPrizePageData struct {
	PageData
	Classrooms  []domain.Classroom
	Prizes      []domain.ClassroomPrize
	Redemptions []domain.PrizeRedemption
	CSRFToken   string
	Message     string
	Error       string
}

func studentPrizeView(w http.ResponseWriter, r *http.Request) {
	user, ok := authenticatedUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if prizeStore == nil || studentStore == nil {
		http.Error(w, "prize store is not configured", http.StatusInternalServerError)
		return
	}
	state, err := prizeStore.LoadStudentPrizeState(r.Context(), user.UserID)
	if err != nil {
		http.Error(w, "could not load classroom prizes", http.StatusInternalServerError)
		return
	}
	studentState, err := studentStore.LoadStudentAvatarState(r.Context(), user)
	if err != nil {
		http.Error(w, "could not load student profile", http.StatusInternalServerError)
		return
	}
	csrfToken, err := getCSRFToken(r)
	if err != nil {
		http.Error(w, "could not load form security token", http.StatusInternalServerError)
		return
	}
	avatar := savedAvatarConfig(studentState.AvatarConfig, studentState.OwnedShopItemIDs)
	status, attendanceMessage, canMark := getTodayAttendanceState(studentState.Attendance, time.Now())
	renderStudent(w, "prizes.html", studentPrizePageData{
		PageData: PageData{Title: "Classroom Prizes", Username: user.Name, Coins: state.CoinBalance,
			AvatarImage: getAvatarImage(avatar), AvatarPreview: buildAvatarPreview(avatar), ActiveNav: "prizes", UseStudentCSS: true,
			AttendanceStatus: status, AttendanceMessage: attendanceMessage, CanMarkAttendance: canMark,
			ThemeBackgroundOptions: ownedThemeBackgroundOptionViews(studentState.OwnedShopItemIDs)},
		Catalogs: state.Catalogs, Redemptions: state.Redemptions, CSRFToken: csrfToken,
		Message: r.URL.Query().Get("msg"), Error: r.URL.Query().Get("error"),
	})
}

func studentPrizePurchase(w http.ResponseWriter, r *http.Request) {
	user, ok := authenticatedUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !parsePrizeForm(w, r) {
		return
	}
	prizeID, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("prize_id")), 10, 64)
	if err != nil || prizeID <= 0 {
		redirectPrize(w, r, "", "Choose a valid prize.")
		return
	}
	redemption, err := prizeStore.PurchaseClassroomPrize(r.Context(), user.UserID, prizeID, time.Now())
	switch {
	case errors.Is(err, datastore.ErrPrizeNotFound), errors.Is(err, datastore.ErrPrizeUnavailable):
		redirectPrize(w, r, "", "That prize is no longer available.")
	case errors.Is(err, datastore.ErrPrizeOutOfStock):
		redirectPrize(w, r, "", "That prize is out of stock.")
	case errors.Is(err, datastore.ErrPrizeClassroomAccess):
		redirectPrize(w, r, "", "That prize is not available to your classroom.")
	case errors.Is(err, datastore.ErrInsufficientCoins):
		redirectPrize(w, r, "", "You do not have enough coins.")
	case err != nil:
		http.Error(w, "could not complete prize purchase", http.StatusInternalServerError)
	default:
		if redemption.NotificationStatus == "no recipient" {
			redirectPrize(w, r, "Prize purchased. It is now pending for your teacher.", "")
		} else {
			redirectPrize(w, r, "Prize purchased. Your teacher has been notified.", "")
		}
	}
}

func teacherPrizeView(w http.ResponseWriter, r *http.Request) {
	user, ok := authenticatedUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if prizeStore == nil {
		http.Error(w, "prize store is not configured", http.StatusInternalServerError)
		return
	}
	state, err := prizeStore.LoadTeacherPrizeState(r.Context(), user.UserID)
	if err != nil {
		http.Error(w, "could not load teacher prizes", http.StatusInternalServerError)
		return
	}
	csrfToken, err := getCSRFToken(r)
	if err != nil {
		http.Error(w, "could not load form security token", http.StatusInternalServerError)
		return
	}
	renderTeacher(w, "teacherPrizes.html", teacherPrizePageData{
		PageData: PageData{Title: "Classroom Prizes", Username: user.Name, HeaderTitle: "Classroom Prizes",
			HeaderSubtitle: "Create real-life rewards and fulfill student purchases.", HeaderBadge: "Teacher View"},
		Classrooms: state.Classrooms, Prizes: state.Prizes, Redemptions: state.Redemptions, CSRFToken: csrfToken,
		Message: r.URL.Query().Get("msg"), Error: r.URL.Query().Get("error"),
	})
}

func teacherPrizeCreate(w http.ResponseWriter, r *http.Request) {
	user, ok := authenticatedUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !parsePrizeForm(w, r) {
		return
	}
	input, err := prizeInputFromRequest(r, false)
	if err != nil {
		redirectTeacherPrizes(w, r, "", err.Error())
		return
	}
	if _, err := prizeStore.CreateClassroomPrize(r.Context(), user.UserID, input); err != nil {
		if errors.Is(err, datastore.ErrPrizeClassroomAccess) {
			redirectTeacherPrizes(w, r, "", "You are not assigned to that classroom.")
			return
		}
		http.Error(w, "could not create prize", http.StatusInternalServerError)
		return
	}
	redirectTeacherPrizes(w, r, "Prize created.", "")
}

func teacherPrizeUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := authenticatedUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !parsePrizeForm(w, r) {
		return
	}
	input, err := prizeInputFromRequest(r, true)
	if err != nil {
		redirectTeacherPrizes(w, r, "", err.Error())
		return
	}
	if err := prizeStore.UpdateClassroomPrize(r.Context(), user.UserID, input); err != nil {
		if errors.Is(err, datastore.ErrPrizeNotFound) {
			redirectTeacherPrizes(w, r, "", "Prize not found or classroom access was removed.")
			return
		}
		http.Error(w, "could not update prize", http.StatusInternalServerError)
		return
	}
	redirectTeacherPrizes(w, r, "Prize updated.", "")
}

func teacherPrizeFulfill(w http.ResponseWriter, r *http.Request) { teacherPrizeTransition(w, r, true) }
func teacherPrizeCancel(w http.ResponseWriter, r *http.Request)  { teacherPrizeTransition(w, r, false) }

func teacherPrizeTransition(w http.ResponseWriter, r *http.Request, fulfill bool) {
	user, ok := authenticatedUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !parsePrizeForm(w, r) {
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("redemption_id")), 10, 64)
	if err != nil || id <= 0 {
		redirectTeacherPrizes(w, r, "", "Choose a valid redemption.")
		return
	}
	if fulfill {
		err = prizeStore.FulfillPrizeRedemption(r.Context(), user.UserID, id, time.Now())
	} else {
		err = prizeStore.CancelPrizeRedemption(r.Context(), user.UserID, id, time.Now())
	}
	if errors.Is(err, datastore.ErrPrizeRedemptionNotPending) {
		redirectTeacherPrizes(w, r, "", "That redemption is no longer pending.")
		return
	}
	if errors.Is(err, datastore.ErrPrizeClassroomAccess) {
		redirectTeacherPrizes(w, r, "", "You are not assigned to that classroom.")
		return
	}
	if err != nil {
		http.Error(w, "could not update redemption", http.StatusInternalServerError)
		return
	}
	if fulfill {
		redirectTeacherPrizes(w, r, "Redemption fulfilled.", "")
	} else {
		redirectTeacherPrizes(w, r, "Redemption canceled and coins refunded.", "")
	}
}

func parsePrizeForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return false
	}
	if !validCSRFToken(r, r.PostFormValue("csrf_token")) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return false
	}
	return true
}

func prizeInputFromRequest(r *http.Request, requireID bool) (domain.PrizeInput, error) {
	input := domain.PrizeInput{ClassroomID: strings.TrimSpace(r.PostFormValue("classroom_id")),
		Name: strings.TrimSpace(r.PostFormValue("name")), Description: strings.TrimSpace(r.PostFormValue("description")),
		Emoji: strings.TrimSpace(r.PostFormValue("emoji")), Active: r.PostFormValue("active") == "true"}
	if requireID {
		id, err := strconv.ParseInt(r.PostFormValue("prize_id"), 10, 64)
		if err != nil || id <= 0 {
			return input, errors.New("choose a valid prize")
		}
		input.ID = id
	}
	if input.ClassroomID == "" {
		return input, errors.New("choose a classroom")
	}
	if count := utf8.RuneCountInString(input.Name); count < 1 || count > 100 {
		return input, errors.New("prize name must be 1 to 100 characters")
	}
	if utf8.RuneCountInString(input.Description) > 500 {
		return input, errors.New("description must be 500 characters or fewer")
	}
	if utf8.RuneCountInString(input.Emoji) > 8 {
		return input, errors.New("emoji must be 8 characters or fewer")
	}
	price, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("coin_price")))
	if err != nil || price < 1 || price > 10000 {
		return input, errors.New("coin price must be between 1 and 10,000")
	}
	input.CoinPrice = price
	if value := strings.TrimSpace(r.PostFormValue("available_stock")); value != "" {
		stock, err := strconv.Atoi(value)
		if err != nil || stock < 0 || stock > 10000 {
			return input, errors.New("stock must be blank or between 0 and 10,000")
		}
		input.AvailableStock = &stock
	}
	return input, nil
}

func redirectPrize(w http.ResponseWriter, r *http.Request, message, errorMessage string) {
	redirectPrizePath(w, r, "/prizes", message, errorMessage)
}
func redirectTeacherPrizes(w http.ResponseWriter, r *http.Request, message, errorMessage string) {
	redirectPrizePath(w, r, "/teacher/prizes", message, errorMessage)
}
func redirectPrizePath(w http.ResponseWriter, r *http.Request, path, message, errorMessage string) {
	query := url.Values{}
	if message != "" {
		query.Set("msg", message)
	}
	if errorMessage != "" {
		query.Set("error", errorMessage)
	}
	http.Redirect(w, r, path+"?"+query.Encode(), http.StatusSeeOther)
}
