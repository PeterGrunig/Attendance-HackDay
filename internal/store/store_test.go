package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/PeterGrunig/Attendance-HackDay/internal/domain"
)

const storeTestDriverName = "attendance_store_test"

var (
	registerStoreTestDriver sync.Once
	storeTestDSNCounter     atomic.Uint64
	storeTestStates         sync.Map
)

// TestFindUserByEmailAndListUsersUseStoredUsers protects the read side of the
// user store: login lookup by email, missing-user behavior, and the admin user
// list. The fake driver stores rows in memory so the assertions should still
// hold while production queries use PostgreSQL syntax.
func TestFindUserByEmailAndListUsersUseStoredUsers(t *testing.T) {
	state := newStoreTestState()
	state.addUser(domain.User{
		UserID:       "student1",
		Name:         "Test Student",
		Role:         "student",
		Email:        "student@example.com",
		PasswordHash: "hash",
		ClassroomID:  "classroom1",
	})
	state.addUser(domain.User{
		UserID: "teacher1",
		Name:   "Test Teacher",
		Role:   "teacher",
		Email:  "teacher@example.com",
	})
	store, _ := newStoreUnderTest(t, state)

	user, err := store.FindUserByEmail(context.Background(), "student@example.com")
	if err != nil {
		t.Fatalf("FindUserByEmail returned error: %v", err)
	}
	if user.UserID != "student1" || user.PasswordHash != "hash" || user.ClassroomID != "classroom1" {
		t.Fatalf("FindUserByEmail returned %#v, want student1 with hash and classroom1", user)
	}

	if _, err := store.FindUserByEmail(context.Background(), "missing@example.com"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("FindUserByEmail missing error = %v, want %v", err, ErrUserNotFound)
	}

	users, err := store.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers returned error: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("ListUsers count = %d, want 2", len(users))
	}
	if users[0].Name != "Test Student" || users[1].Name != "Test Teacher" {
		t.Fatalf("ListUsers returned %#v, want users ordered by name", users)
	}
}

// TestCreateStudentPersistsUserAndClassroomAssignment covers the two writes
// that make a student useful in the app: inserting the user row and linking the
// student to an existing classroom through ClassroomStudents.
func TestCreateStudentPersistsUserAndClassroomAssignment(t *testing.T) {
	state := newStoreTestState()
	state.addClassroom(domain.Classroom{ID: "classroom1", Name: "First Grade"})
	store, state := newStoreUnderTest(t, state)

	student := domain.User{
		UserID:       "student1",
		Name:         "Test Student",
		Role:         "student",
		Email:        "student@example.com",
		PasswordHash: "hash",
		ClassroomID:  "classroom1",
	}
	if err := store.CreateStudent(context.Background(), student); err != nil {
		t.Fatalf("CreateStudent returned error: %v", err)
	}
	if got, ok := state.users["student1"]; !ok || got.Email != "student@example.com" {
		t.Fatalf("CreateStudent stored user = %#v, %v; want student1", got, ok)
	}
	if !state.hasClassroomStudent("classroom1", "student1") {
		t.Fatal("CreateStudent did not store classroom assignment")
	}

	if err := store.CreateStudent(context.Background(), student); !errors.Is(err, ErrUserAlreadyExists) {
		t.Fatalf("CreateStudent duplicate error = %v, want %v", err, ErrUserAlreadyExists)
	}

	missingClassroomStudent := student
	missingClassroomStudent.UserID = "student2"
	missingClassroomStudent.Email = "student2@example.com"
	missingClassroomStudent.ClassroomID = "missing"
	if err := store.CreateStudent(context.Background(), missingClassroomStudent); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("CreateStudent missing classroom error = %v, want %v", err, ErrClassroomNotFound)
	}
	if _, ok := state.users["student2"]; ok {
		t.Fatal("CreateStudent stored a user after classroom validation failed")
	}
}

// TestCreateTeacherPersistsTeacherAndRejectsDuplicate verifies that teachers
// are stored as normal users and that the store reports duplicate IDs instead
// of silently overwriting existing rows.
func TestCreateTeacherPersistsTeacherAndRejectsDuplicate(t *testing.T) {
	state := newStoreTestState()
	store, state := newStoreUnderTest(t, state)

	teacher := domain.User{
		UserID:       "teacher1",
		Name:         "Test Teacher",
		Role:         "teacher",
		Email:        "teacher@example.com",
		PasswordHash: "hash",
	}
	if err := store.CreateTeacher(context.Background(), teacher); err != nil {
		t.Fatalf("CreateTeacher returned error: %v", err)
	}
	if got := state.users["teacher1"]; got.Role != "teacher" {
		t.Fatalf("CreateTeacher stored role = %q, want teacher", got.Role)
	}

	if err := store.CreateTeacher(context.Background(), teacher); !errors.Is(err, ErrUserAlreadyExists) {
		t.Fatalf("CreateTeacher duplicate error = %v, want %v", err, ErrUserAlreadyExists)
	}
}

// TestCreateAndUpdateClassroomMaintainRoster protects classroom editing during
// the migration. Updating a classroom should modify the existing classroom and
// replace its roster, not create a second classroom record.
func TestCreateAndUpdateClassroomMaintainRoster(t *testing.T) {
	state := newStoreTestState()
	store, state := newStoreUnderTest(t, state)

	classroom := domain.Classroom{
		ID:         "classroom1",
		Name:       "First Grade",
		TeacherID:  "teacher1",
		StudentIDs: []string{"student1", "student2"},
	}
	if err := store.CreateClassroom(context.Background(), classroom); err != nil {
		t.Fatalf("CreateClassroom returned error: %v", err)
	}
	if got := state.classrooms["classroom1"]; got.Name != "First Grade" || got.TeacherID != "teacher1" {
		t.Fatalf("CreateClassroom stored classroom = %#v", got)
	}
	if !state.hasClassroomStudent("classroom1", "student1") || !state.hasClassroomStudent("classroom1", "student2") {
		t.Fatal("CreateClassroom did not store initial roster")
	}

	updated := domain.Classroom{
		ID:         "classroom-renamed",
		Name:       "First Grade Updated",
		TeacherID:  "teacher2",
		StudentIDs: []string{"student2", "student3"},
	}
	if err := store.UpdateClassroom(context.Background(), "classroom1", updated); err != nil {
		t.Fatalf("UpdateClassroom returned error: %v", err)
	}
	if _, ok := state.classrooms["classroom1"]; ok {
		t.Fatal("UpdateClassroom left the original classroom ID behind")
	}
	if got := state.classrooms["classroom-renamed"]; got.Name != "First Grade Updated" || got.TeacherID != "teacher2" {
		t.Fatalf("UpdateClassroom stored classroom = %#v", got)
	}
	if state.hasClassroomStudent("classroom-renamed", "student1") {
		t.Fatal("UpdateClassroom kept a removed student assignment")
	}
	if !state.hasClassroomStudent("classroom-renamed", "student2") || !state.hasClassroomStudent("classroom-renamed", "student3") {
		t.Fatal("UpdateClassroom did not replace roster with submitted students")
	}

	if err := store.UpdateClassroom(context.Background(), "missing", updated); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("UpdateClassroom missing error = %v, want %v", err, ErrClassroomNotFound)
	}
}

// TestListClassroomsAggregatesRosterRows makes sure ListClassrooms rebuilds the
// domain Classroom values from classroom rows plus ClassroomStudents join rows.
// That behavior matters more than the exact PostgreSQL query formatting.
func TestListClassroomsAggregatesRosterRows(t *testing.T) {
	state := newStoreTestState()
	state.addClassroom(domain.Classroom{ID: "classroom2", Name: "Second Grade", TeacherID: "teacher2"})
	state.addClassroom(domain.Classroom{ID: "classroom1", Name: "First Grade", TeacherID: "teacher1"})
	state.addClassroomStudent("classroom1", "student2")
	state.addClassroomStudent("classroom1", "student1")
	store, _ := newStoreUnderTest(t, state)

	classrooms, err := store.ListClassrooms(context.Background())
	if err != nil {
		t.Fatalf("ListClassrooms returned error: %v", err)
	}
	if len(classrooms) != 2 {
		t.Fatalf("ListClassrooms count = %d, want 2", len(classrooms))
	}
	if classrooms[0].ID != "classroom1" || classrooms[1].ID != "classroom2" {
		t.Fatalf("ListClassrooms order = %#v, want classroom1 then classroom2", classrooms)
	}
	if got := strings.Join(classrooms[0].StudentIDs, ","); got != "student1,student2" {
		t.Fatalf("classroom1 student IDs = %q, want student1,student2", got)
	}
	if len(classrooms[1].StudentIDs) != 0 {
		t.Fatalf("classroom2 student IDs = %#v, want none", classrooms[1].StudentIDs)
	}
}

// TestUpdateUserRolePersistsRoleAndRejectsMissingUser checks the user settings
// write path: role changes must persist, and missing users must surface as
// ErrUserNotFound so handlers can show the right failure message.
func TestUpdateUserRolePersistsRoleAndRejectsMissingUser(t *testing.T) {
	state := newStoreTestState()
	state.addUser(domain.User{UserID: "student1", Name: "Test Student", Role: "student"})
	store, state := newStoreUnderTest(t, state)

	if err := store.UpdateUserRole(context.Background(), "student1", "teacher"); err != nil {
		t.Fatalf("UpdateUserRole returned error: %v", err)
	}
	if got := state.users["student1"].Role; got != "teacher" {
		t.Fatalf("UpdateUserRole stored role = %q, want teacher", got)
	}

	if err := store.UpdateUserRole(context.Background(), "missing", "admin"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("UpdateUserRole missing error = %v, want %v", err, ErrUserNotFound)
	}
}

type storeTestState struct {
	users             map[string]domain.User
	classrooms        map[string]domain.Classroom
	classroomStudents map[string]map[string]bool
}

func newStoreTestState() *storeTestState {
	return &storeTestState{
		users:             map[string]domain.User{},
		classrooms:        map[string]domain.Classroom{},
		classroomStudents: map[string]map[string]bool{},
	}
}

func (s *storeTestState) addUser(user domain.User) {
	s.users[user.UserID] = user
}

func (s *storeTestState) addClassroom(classroom domain.Classroom) {
	s.classrooms[classroom.ID] = classroom
}

func (s *storeTestState) addClassroomStudent(classroomID string, studentID string) {
	if s.classroomStudents[classroomID] == nil {
		s.classroomStudents[classroomID] = map[string]bool{}
	}
	s.classroomStudents[classroomID][studentID] = true
}

func (s *storeTestState) hasClassroomStudent(classroomID string, studentID string) bool {
	return s.classroomStudents[classroomID] != nil && s.classroomStudents[classroomID][studentID]
}

func (s *storeTestState) clone() *storeTestState {
	copied := newStoreTestState()
	for id, user := range s.users {
		copied.users[id] = user
	}
	for id, classroom := range s.classrooms {
		copied.classrooms[id] = classroom
	}
	for classroomID, students := range s.classroomStudents {
		copied.classroomStudents[classroomID] = map[string]bool{}
		for studentID, assigned := range students {
			copied.classroomStudents[classroomID][studentID] = assigned
		}
	}
	return copied
}

func (s *storeTestState) replaceFrom(other *storeTestState) {
	s.users = other.users
	s.classrooms = other.classrooms
	s.classroomStudents = other.classroomStudents
}

// newStoreUnderTest connects SQLStore to the in-memory test driver. The store
// still goes through database/sql, but the driver classifies queries by intent
// instead of requiring one exact SQL dialect.
func newStoreUnderTest(t *testing.T, state *storeTestState) (*SQLStore, *storeTestState) {
	t.Helper()

	registerStoreTestDriver.Do(func() {
		sql.Register(storeTestDriverName, storeTestDriver{})
	})

	dsn := fmt.Sprintf("%s-%d", t.Name(), storeTestDSNCounter.Add(1))
	storeTestStates.Store(dsn, state)
	t.Cleanup(func() {
		storeTestStates.Delete(dsn)
	})

	db, err := sql.Open(storeTestDriverName, dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close test db: %v", err)
		}
	})

	return NewSQLStore(db), state
}

type storeTestDriver struct{}

func (storeTestDriver) Open(name string) (driver.Conn, error) {
	value, ok := storeTestStates.Load(name)
	if !ok {
		return nil, fmt.Errorf("test database state %q not found", name)
	}
	return &storeTestConn{root: value.(*storeTestState)}, nil
}

type storeTestConn struct {
	root   *storeTestState
	active *storeTestState
}

func (c *storeTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not used in store tests")
}

func (c *storeTestConn) Close() error {
	return nil
}

func (c *storeTestConn) Begin() (driver.Tx, error) {
	c.active = c.root.clone()
	return &storeTestTx{conn: c}, nil
}

func (c *storeTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}

func (c *storeTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	state := c.state()
	normalized := normalizeStoreTestQuery(query)

	switch {
	case strings.Contains(normalized, "from users") && strings.Contains(normalized, "where email"):
		return state.queryUserByEmail(namedValueString(args, 0)), nil
	case strings.Contains(normalized, "select 1") && strings.Contains(normalized, "from classrooms"):
		return state.queryClassroomExists(namedValueString(args, 0)), nil
	case strings.Contains(normalized, "select 1") && strings.Contains(normalized, "from users"):
		return state.queryUserExists(namedValueString(args, 0)), nil
	case strings.Contains(normalized, "from classrooms") && strings.Contains(normalized, "classroommemberships"):
		return state.queryClassroomsWithMemberships(), nil
	case strings.Contains(normalized, "from users"):
		return state.queryUsers(), nil
	default:
		return nil, fmt.Errorf("unsupported query: %s", query)
	}
}

func (c *storeTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	state := c.state()
	normalized := normalizeStoreTestQuery(query)

	switch {
	case strings.Contains(normalized, "insert into users"):
		return state.execInsertUser(args)
	case strings.Contains(normalized, "insert into classrooms") && !strings.Contains(normalized, "classroomstudents"):
		return state.execInsertClassroom(args)
	case strings.Contains(normalized, "update classrooms"):
		return state.execUpdateClassroom(args)
	case strings.Contains(normalized, "delete from classroomstudents"):
		return state.execDeleteClassroomStudents(args)
	case strings.Contains(normalized, "delete from classroommemberships"):
		return storeTestResult(1), nil
	case strings.Contains(normalized, "insert into classroomstudents"):
		return state.execInsertClassroomStudent(args)
	case strings.Contains(normalized, "insert into classroommemberships"):
		return state.execInsertClassroomMembership(normalized, args)
	case strings.Contains(normalized, "update users") && strings.Contains(normalized, "set role"):
		return state.execUpdateUserRole(args)
	case strings.Contains(normalized, "update users") && strings.Contains(normalized, "set classroomid"):
		return state.execAssignUserClassroom(args)
	default:
		return nil, fmt.Errorf("unsupported exec: %s", query)
	}
}

func (c *storeTestConn) state() *storeTestState {
	if c.active != nil {
		return c.active
	}
	return c.root
}

// storeTestTx gives the fake driver transaction behavior close enough for the
// store tests: writes happen against a cloned state and only become visible on
// Commit.
type storeTestTx struct {
	conn *storeTestConn
}

func (tx *storeTestTx) Commit() error {
	tx.conn.root.replaceFrom(tx.conn.active)
	tx.conn.active = nil
	return nil
}

func (tx *storeTestTx) Rollback() error {
	tx.conn.active = nil
	return nil
}

func (s *storeTestState) queryUserByEmail(email string) driver.Rows {
	for _, user := range sortedUsers(s.users) {
		if user.Email == email {
			return storeTestRows(
				[]string{"UserID", "Name", "Role", "Email", "PasswordHash", "ClassroomID"},
				[][]driver.Value{{user.UserID, user.Name, user.Role, user.Email, user.PasswordHash, user.ClassroomID}},
			)
		}
	}
	return storeTestRows([]string{"UserID", "Name", "Role", "Email", "PasswordHash", "ClassroomID"}, nil)
}

func (s *storeTestState) queryClassroomExists(classroomID string) driver.Rows {
	if _, ok := s.classrooms[classroomID]; ok {
		return storeTestRows([]string{"exists"}, [][]driver.Value{{int64(1)}})
	}
	return storeTestRows([]string{"exists"}, nil)
}

func (s *storeTestState) queryUserExists(userID string) driver.Rows {
	if _, ok := s.users[userID]; ok {
		return storeTestRows([]string{"exists"}, [][]driver.Value{{int64(1)}})
	}
	return storeTestRows([]string{"exists"}, nil)
}

func (s *storeTestState) queryClassroomsWithMemberships() driver.Rows {
	rows := [][]driver.Value{}
	for _, classroom := range sortedClassrooms(s.classrooms) {
		studentIDs := sortedStudentIDs(s.classroomStudents[classroom.ID])
		if len(studentIDs) == 0 && classroom.TeacherID == "" {
			rows = append(rows, []driver.Value{classroom.ID, classroom.Name, "", "", false, ""})
		}
		for _, studentID := range studentIDs {
			rows = append(rows, []driver.Value{classroom.ID, classroom.Name, studentID, "student", true, ""})
		}
		if classroom.TeacherID != "" {
			rows = append(rows, []driver.Value{classroom.ID, classroom.Name, classroom.TeacherID, "teacher", true, ""})
		}
	}
	return storeTestRows([]string{"ID", "Name", "UserID", "MembershipRole", "IsPrimary", "ManagedBy"}, rows)
}

func (s *storeTestState) queryUsers() driver.Rows {
	rows := [][]driver.Value{}
	for _, user := range sortedUsers(s.users) {
		rows = append(rows, []driver.Value{user.UserID, user.Name, user.Role, user.Email, user.ClassroomID})
	}
	return storeTestRows([]string{"UserID", "Name", "Role", "Email", "ClassroomID"}, rows)
}

// execInsertUser intentionally enforces duplicate user IDs so tests can verify
// the store translates those failures into its public error values.
func (s *storeTestState) execInsertUser(args []driver.NamedValue) (driver.Result, error) {
	user := domain.User{
		UserID:       namedValueString(args, 0),
		Name:         namedValueString(args, 1),
		Role:         namedValueString(args, 2),
		Email:        namedValueString(args, 3),
		PasswordHash: namedValueString(args, 4),
		ClassroomID:  namedValueString(args, 5),
	}
	if _, exists := s.users[user.UserID]; exists {
		return storeTestResult(0), fmt.Errorf("user %q already exists", user.UserID)
	}
	s.users[user.UserID] = user
	return storeTestResult(1), nil
}

func (s *storeTestState) execInsertClassroom(args []driver.NamedValue) (driver.Result, error) {
	classroom := domain.Classroom{
		ID:        namedValueString(args, 0),
		Name:      namedValueString(args, 1),
		TeacherID: namedValueString(args, 2),
	}
	if _, exists := s.classrooms[classroom.ID]; exists {
		return storeTestResult(0), fmt.Errorf("classroom %q already exists", classroom.ID)
	}
	s.classrooms[classroom.ID] = classroom
	return storeTestResult(1), nil
}

// execUpdateClassroom mirrors the store's expected argument order and row-count
// behavior. Returning zero rows lets the production store surface
// ErrClassroomNotFound.
func (s *storeTestState) execUpdateClassroom(args []driver.NamedValue) (driver.Result, error) {
	originalID := namedValueString(args, 0)
	classroom, exists := s.classrooms[originalID]
	if !exists {
		return storeTestResult(0), nil
	}

	newID := namedValueString(args, 1)
	classroom.ID = newID
	classroom.Name = namedValueString(args, 2)
	classroom.TeacherID = namedValueString(args, 3)
	delete(s.classrooms, originalID)
	s.classrooms[newID] = classroom

	if students, ok := s.classroomStudents[originalID]; ok {
		delete(s.classroomStudents, originalID)
		s.classroomStudents[newID] = students
	}

	return storeTestResult(1), nil
}

// execDeleteClassroomStudents accepts both the old and new classroom IDs because
// update flows may delete roster rows before or after a classroom ID changes.
func (s *storeTestState) execDeleteClassroomStudents(args []driver.NamedValue) (driver.Result, error) {
	deleted := int64(0)
	for _, classroomID := range []string{namedValueString(args, 0), namedValueString(args, 1)} {
		if students := s.classroomStudents[classroomID]; students != nil {
			deleted += int64(len(students))
			delete(s.classroomStudents, classroomID)
		}
	}
	return storeTestResult(deleted), nil
}

func (s *storeTestState) execInsertClassroomStudent(args []driver.NamedValue) (driver.Result, error) {
	classroomID := namedValueString(args, 0)
	studentID := namedValueString(args, 1)
	if s.classroomStudents[classroomID] == nil {
		s.classroomStudents[classroomID] = map[string]bool{}
	}
	if s.classroomStudents[classroomID][studentID] {
		return storeTestResult(0), nil
	}
	s.classroomStudents[classroomID][studentID] = true
	return storeTestResult(1), nil
}

func (s *storeTestState) execInsertClassroomMembership(normalized string, args []driver.NamedValue) (driver.Result, error) {
	classroomID := namedValueString(args, 0)
	userID := namedValueString(args, 1)
	if strings.Contains(normalized, "'teacher'") {
		classroom := s.classrooms[classroomID]
		classroom.TeacherID = userID
		s.classrooms[classroomID] = classroom
		return storeTestResult(1), nil
	}
	if strings.Contains(normalized, "'student'") {
		return s.execInsertClassroomStudent(args)
	}
	return nil, fmt.Errorf("unsupported classroom membership: %s", normalized)
}

func (s *storeTestState) execAssignUserClassroom(args []driver.NamedValue) (driver.Result, error) {
	userID := namedValueString(args, 0)
	user, ok := s.users[userID]
	if !ok || user.ClassroomID != "" {
		return storeTestResult(0), nil
	}
	user.ClassroomID = namedValueString(args, 1)
	s.users[userID] = user
	return storeTestResult(1), nil
}

func (s *storeTestState) execUpdateUserRole(args []driver.NamedValue) (driver.Result, error) {
	userID := namedValueString(args, 0)
	user, ok := s.users[userID]
	if !ok {
		return storeTestResult(0), nil
	}
	user.Role = namedValueString(args, 1)
	s.users[userID] = user
	return storeTestResult(1), nil
}

type storeTestResult int64

func (r storeTestResult) LastInsertId() (int64, error) {
	return 0, errors.New("LastInsertId is not used in store tests")
}

func (r storeTestResult) RowsAffected() (int64, error) {
	return int64(r), nil
}

// storeTestRowsData is the minimal driver.Rows implementation needed by
// database/sql Scan calls in the store functions.
type storeTestRowsData struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func storeTestRows(columns []string, values [][]driver.Value) *storeTestRowsData {
	return &storeTestRowsData{columns: columns, values: values}
}

func (r *storeTestRowsData) Columns() []string {
	return r.columns
}

func (r *storeTestRowsData) Close() error {
	return nil
}

func (r *storeTestRowsData) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

// normalizeStoreTestQuery removes formatting so the fake driver recognizes
// PostgreSQL store operations by intent rather than exact whitespace.
func normalizeStoreTestQuery(query string) string {
	normalized := strings.ToLower(query)
	normalized = strings.ReplaceAll(normalized, `"`, "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.Join(strings.Fields(normalized), " ")
	return normalized
}

func namedValueString(args []driver.NamedValue, index int) string {
	if index >= len(args) || args[index].Value == nil {
		return ""
	}
	return fmt.Sprint(args[index].Value)
}

func sortedUsers(users map[string]domain.User) []domain.User {
	sorted := make([]domain.User, 0, len(users))
	for _, user := range users {
		sorted = append(sorted, user)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Name == sorted[j].Name {
			return sorted[i].UserID < sorted[j].UserID
		}
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

func sortedClassrooms(classrooms map[string]domain.Classroom) []domain.Classroom {
	sorted := make([]domain.Classroom, 0, len(classrooms))
	for _, classroom := range classrooms {
		sorted = append(sorted, classroom)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})
	return sorted
}

func sortedStudentIDs(students map[string]bool) []string {
	sorted := make([]string, 0, len(students))
	for studentID := range students {
		sorted = append(sorted, studentID)
	}
	sort.Strings(sorted)
	return sorted
}
