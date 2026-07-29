# Project TODO

Two-person workflow. **Do the "Do Together First" section before splitting.**

---

## Do Together First (~20–30 min)

These must be agreed on before either person starts their track. Commit the results immediately so both people are unblocked.

- [x] Define all shared structs in `types.go` — `User` (with role field), `Classroom`, `CoinTransaction`, `ShopItem`, `AvatarConfig`
- [x] Decide on roles as constants: `student`, `teacher`, `admin`
- [x] Agree on JSON filenames: `users.json`, `classrooms.json`, `shop.json`, `attendance.json`, `transactions.json`
- [x] Sketch and write down every route, the HTTP method, and which role(s) can access it
- [x] Commit `types.go` and the agreed route list so both people can reference them

---

## Track A — Foundation (Person 1)

### Project Scaffold
- [x] `go mod init`
- [x] Set up folder structure: `main.go`, `store/`, `handlers/`, `templates/`, `static/`
- [x] `main.go` with a basic HTTP server and static file serving

### Persistence
- [x] Use PostgreSQL as the sole runtime data source
- [x] Load application state through SQL store methods
- [x] Persist application state through SQL store methods
- [x] Remove `data.json` and filesystem persistence
- [x] Keep authentication session tokens in memory
- [x] Load the authenticated SQL user into request context by user ID

### User Storage
- [x] Seed test users (student, teacher, admin)
- [x] Password hashing (`bcrypt`)
- [x] Password verification during login

### Auth Routes
- [x] `POST /login` — validate credentials, create session
- [x] `POST /logout` — destroy session
- [x] `POST /register` — if needed for admin setup

### Session Management
- [x] Cookie-based session helpers
- [x] In-memory session store
- [x] Secure session IDs
- [x] Session expiration

### Auth Middleware
- [x] `requireLogin` middleware — redirects to `/login` if no valid session
- [x] `requireRole(role string)` middleware — returns 403 if role doesn't match

**Track A Deliverable:** Running server where you can log in, get a session cookie, and log out. Middleware functions exported and ready.

---

## Track B — UI Shell (Person 2)

### Setup
- [x] Static file serving confirmed working
- [x] `html/template` base layout with a `{{template "content" .}}` slot
- [x] CSS scaffold (can use a small utility CSS or write from scratch)
- [x] Add a `fakeUser` variable at the top of the handlers file so you can swap it for a real session lookup in one line later

### Stub Pages — All Roles
- [x] `/login` — login form page (full page)
- [x] Logout — popup/modal component

### Stub Pages — Student
- [x] `/student/coins` — receive coins popup
- [x] `/student/shop` — shop full page
- [x] `/student/avatar` — avatar customization full page

### Stub Pages — Teacher
- [x] `/teacher/attendance` — attendance full page
- [x] `/teacher/reports` — reports full page
- [x] `/teacher/schedule` — schedule / double days full page

### Stub Pages — Admin
- [x] `/admin/reports` — all reports full page
- [x] `/admin/classrooms` — classrooms full page

### Navigation
- [x] Header component with role-aware nav links (use `fakeUser.Role` to show/hide links)
- [x] Make sure nav links match the agreed route list

### Shared Components
- [x] Form styles and button styles
- [x] Table component (used in attendance, reports, classrooms)
- [x] Popup/modal component (used for logout, receive coins)
- [x] Flash/error message display for form feedback

**Track B Deliverable:** Every page reachable, looks roughly right, using the hardcoded fake user.

---

## Track A — Student Experience

### Coins / Attendance
- [x] Wire "Mark Attendance" button to backend
- [x] `POST /attendance`
- [x] Award coins when attendance is submitted
- [x] Persist coin awards as `CoinTransaction` entries
- [x] Save attendance and its coin reward in one SQL transaction
- [x] Compute coin balance from transaction history
- [x] Prevent displayed and spendable coin balances from going negative
- [x] Support manual coin adjustments from `ManualCoinAdjustments`
- [x] Double day multiplier support

### Student Dashboard
- [x] Display current coin balance
- [x] Display attendance status
- [x] Display avatar
- [x] Show upcoming double days

### Shop
- [x] Seed shop items
- [x] `GET /shop`
- [x] Render shop items
- [x] Split shop into Avatar Items and Backgrounds sections
- [x] `POST /shop/buy`
- [x] Validate sufficient coins
- [x] Persist ownership and coin debits in one SQL transaction
- [x] Show owned items
- [x] Update balance after purchases
- [x] Seed visual avatar cosmetics with shop previews
- [x] Seed purchasable pastel background themes
- [x] Show purchased backgrounds in owned inventory
- [x] Unlock purchased backgrounds in the theme picker

### Avatar System
- [x] Persist avatar selections with an SQL upsert
- [x] Display available avatar options
- [x] Display owned/unlocked cosmetics
- [x] Preview avatar changes
- [x] Save avatar changes
- [x] Show avatar on dashboard/navbar
- [x] Normalize base avatar images for consistent display
- [x] Add all current avatar images as selectable base avatars
- [x] Add Peter, Funk Rapper, and Gopher base avatars
- [x] Fix Funk Rapper orientation and Brazil flag background
- [x] Add visual cosmetic overlays to avatar preview and saved avatar display
- [x] Support avatar effect cosmetics

### Student Navbar
- [x] Display avatar
- [x] Display username
- [x] Display coin balance
- [x] Use real user data

### Student Theme / CSS Polish
- [x] Calm student-side CSS refresh across dashboard, shop, avatar page, and related components
- [x] Add persistent light/dark mode toggle
- [x] Add free background color options: red, blue, green, yellow, orange, pink, purple
- [x] Add sparse pastel background patterns
- [x] Add purchasable special backgrounds: beach, forest, sky, meadow, mountain, sunset
- [x] Keep purchased special backgrounds locked until bought

**Track A Deliverable:** Student can log in, mark attendance, earn coins, view schedule, buy items, customize avatar, and see all data reflected throughout the UI.

---

## Track B — Teacher / Admin Management

### Classroom Management
- [x] `POST /classrooms`
- [x] `GET /classrooms`
- [x] Create classrooms with teacher and student assignments
- [x] Edit classrooms and replace teacher/student assignments
- [x] Display classroom assignments on the admin dashboard
- [ ] Validate assignments

### Teacher Management
- [x] Admin creates teachers
- [ ] Teachers create students in assigned classroom
- [ ] Admin inherits teacher permissions

### Student / User Management
- [x] Admin creates students in a selected classroom
- [x] Validate the selected classroom when creating a student
- [x] Search and display users in user settings
- [x] Update user roles
- [x] Prevent removal of the current or last admin account

### Schedule / Double Days
- [ ] `POST /schedule/doubleday`
- [ ] `GET /schedule`
- [ ] Persist schedule changes
- [ ] Prevent duplicate entries
- [ ] Teacher schedule management UI

### Reports
- [ ] Teacher dashboard class reports
- [ ] Attendance reporting endpoints
- [ ] CSV export endpoint
- [ ] Admin reports across all classrooms
- [ ] Admin reports grouped by teacher

### Teacher Dashboard
- [ ] Attendance management UI
- [ ] Student roster UI
- [ ] Create student UI
- [ ] Class reports UI
- [ ] Schedule management UI

### Admin Dashboard
- [x] Display classroom, student, and teacher summaries
- [x] Create classroom UI
- [x] Edit classroom UI
- [x] Create teacher UI
- [x] Create student UI
- [x] Assign teacher UI
- [x] Assign student UI
- [x] User settings and role management UI
- [ ] School-wide reports UI

### Shared Navigation
- [ ] Role-aware navbar behavior
- [ ] Teacher navigation
- [x] Admin navigation

**Track B Deliverable:** Teachers and admins can manage classes, students, schedules, attendance, and reports entirely from their dashboards.

---

## Final Packaging

- [ ] Test on a clean machine (no Go installed) to verify the binary is self-contained
- [x] Embed templates and static files using Go's `embed` package so they're bundled into the binary
- [ ] Build for Windows: `GOOS=windows GOARCH=amd64 go build -o app.exe`
- [ ] Confirm the `.exe` opens the server and can be visited at `localhost:<port>`
- [ ] Optionally: auto-open the browser on launch using `os/exec` to call `start` (Windows) or `open` (Mac)
- [ ] Write a short README with instructions for running the `.exe`
