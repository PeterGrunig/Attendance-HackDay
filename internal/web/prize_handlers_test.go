package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
)

type prizeStoreStub struct{ purchaseCalls int }

func (*prizeStoreStub) LoadStudentPrizeState(context.Context, string) (domain.StudentPrizeState, error) {
	return domain.StudentPrizeState{}, nil
}
func (*prizeStoreStub) LoadTeacherPrizeState(context.Context, string) (domain.TeacherPrizeState, error) {
	return domain.TeacherPrizeState{}, nil
}
func (s *prizeStoreStub) PurchaseClassroomPrize(context.Context, string, int64, time.Time) (domain.PrizeRedemption, error) {
	s.purchaseCalls++
	return domain.PrizeRedemption{}, nil
}
func (*prizeStoreStub) CreateClassroomPrize(context.Context, string, domain.PrizeInput) (int64, error) {
	return 1, nil
}
func (*prizeStoreStub) UpdateClassroomPrize(context.Context, string, domain.PrizeInput) error {
	return nil
}
func (*prizeStoreStub) FulfillPrizeRedemption(context.Context, string, int64, time.Time) error {
	return nil
}
func (*prizeStoreStub) CancelPrizeRedemption(context.Context, string, int64, time.Time) error {
	return nil
}

func TestPrizeInputValidation(t *testing.T) {
	form := url.Values{"classroom_id": {"class-1"}, "name": {"Snack"}, "description": {"A classroom snack"}, "emoji": {"🍎"}, "coin_price": {"5"}, "available_stock": {"3"}, "active": {"true"}}
	request := httptest.NewRequest(http.MethodPost, "/teacher/prizes/create", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = request.ParseForm()
	input, err := prizeInputFromRequest(request, false)
	if err != nil {
		t.Fatalf("prizeInputFromRequest: %v", err)
	}
	if input.Name != "Snack" || input.AvailableStock == nil || *input.AvailableStock != 3 || !input.Active {
		t.Fatalf("input = %#v", input)
	}

	form.Set("coin_price", "0")
	request = httptest.NewRequest(http.MethodPost, "/teacher/prizes/create", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = request.ParseForm()
	if _, err := prizeInputFromRequest(request, false); err == nil {
		t.Fatal("zero coin price passed validation")
	}
}

func TestStudentPrizePurchaseRejectsInvalidCSRF(t *testing.T) {
	previous := prizeStore
	stub := &prizeStoreStub{}
	prizeStore = stub
	t.Cleanup(func() { prizeStore = previous })
	form := url.Values{"prize_id": {"1"}, "csrf_token": {"wrong"}}
	request := httptest.NewRequest(http.MethodPost, "/prizes/purchase", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = withAuthenticatedUser(request, domain.User{UserID: "student-1", Role: "student"})
	recorder := httptest.NewRecorder()
	studentPrizePurchase(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if stub.purchaseCalls != 0 {
		t.Fatal("purchase store called after CSRF rejection")
	}
}

func TestPrizePagesRenderPopulatedStates(t *testing.T) {
	stock := 0
	studentTemplate, err := loadStudentTemplates("prizes.html")
	if err != nil {
		t.Fatalf("load student prizes: %v", err)
	}
	studentData := studentPrizePageData{PageData: PageData{Title: "Prizes", UseStudentCSS: true}, CSRFToken: "csrf",
		Catalogs:    []domain.PrizeCatalog{{ClassroomName: "Room 1", Prizes: []domain.ClassroomPrize{{ID: 1, Name: "Snack", Emoji: "🍎", CoinPrice: 5, AvailableStock: &stock}}}},
		Redemptions: []domain.PrizeRedemption{{ID: 2, PrizeName: "Snack", Status: "pending", PurchasedAt: time.Now()}}}
	var rendered bytes.Buffer
	if err := studentTemplate.ExecuteTemplate(&rendered, "base", studentData); err != nil {
		t.Fatalf("render student prizes: %v", err)
	}
	if !strings.Contains(rendered.String(), "Out of stock") {
		t.Fatal("student page did not render finite zero stock")
	}

	teacherTemplate, err := loadTeacherTemplates("teacherPrizes.html")
	if err != nil {
		t.Fatalf("load teacher prizes: %v", err)
	}
	rendered.Reset()
	teacherData := teacherPrizePageData{PageData: PageData{Title: "Prizes"}, CSRFToken: "csrf",
		Classrooms: []domain.Classroom{{ID: "class-1", Name: "Room 1"}}, Prizes: []domain.ClassroomPrize{{ID: 1, ClassroomID: "class-1", ClassroomName: "Room 1", Name: "Snack", CoinPrice: 5, Active: true}},
		Redemptions: []domain.PrizeRedemption{{ID: 2, PrizeName: "Snack", StudentName: "Student", Status: "pending", PurchasedAt: time.Now()}}}
	if err := teacherTemplate.ExecuteTemplate(&rendered, "base", teacherData); err != nil {
		t.Fatalf("render teacher prizes: %v", err)
	}
	if !strings.Contains(rendered.String(), "Cancel &amp; refund") {
		t.Fatal("teacher page did not render pending actions")
	}
}
