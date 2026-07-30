package domain

type User struct {
	Name         string `json:"name"`
	Role         string `json:"role"` // "student", "teacher", "admin"
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash"`
	ClassroomID  string `json:"classroom_id"` // for students, which classroom they belong to
	UserID       string `json:"user_id"`
}

type Classroom struct {
	Name       string   `json:"name"`
	ID         string   `json:"id"`
	TeacherID  string   `json:"teacher_id"`
	TeacherIDs []string `json:"teacher_ids,omitempty"`
	StudentIDs []string `json:"student_ids"`
	ManagedBy  string   `json:"managed_by,omitempty"`
}

type Schedule struct {
	ClassroomID string `json:"classroom_id"`
	DayOfWeek   string `json:"day_of_week"` // "Monday", "Tuesday", etc.
	StartTime   string `json:"start_time"`  // "09:00"
	EndTime     string `json:"end_time"`    // "10:00"
	DoubleDay   bool   `json:"double_day"`  // true if this schedule is a double day
}

type ShopItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Price       int    `json:"price"` // price in coins
	Description string `json:"description"`
	ImagePath   string `json:"image_path"`
	Slot        string `json:"slot"`
}

type AvatarConfig struct {
	Base      string `json:"base"`
	HairStyle string `json:"hair_style"`
	Clothing  string `json:"clothing"`
	Accessory string `json:"accessory"`
	Effect    string `json:"effect"`
}

type CoinTransaction struct {
	UserID      string `json:"user_id"`
	Amount      int    `json:"amount"` // positive for earning coins, negative for spending coins
	Timestamp   string `json:"timestamp"`
	Description string `json:"description"`
}

type AttendanceRecord struct {
	UserID      string   `json:"user_id"`
	ClassroomID string   `json:"classroom_id"`
	Present     []string `json:"present"` // list of dates when the student was present
	Absent      []string `json:"absent"`  // list of dates when the student was absent
}

// StudentState contains the SQL-backed data shared by the student dashboard,
// shop, and avatar pages for one authenticated student.
type StudentState struct {
	User             User
	CoinBalance      int
	Attendance       AttendanceRecord
	Schedules        []Schedule
	ShopItems        []ShopItem
	OwnedShopItemIDs []string
	AvatarConfig     *AvatarConfig
}
