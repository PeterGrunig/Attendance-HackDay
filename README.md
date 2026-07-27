# Attendance-HackDay

Attendance Quest is a Go server-rendered attendance rewards app for students,
teachers, and admins. Students can log in, mark attendance, earn coins, buy shop
items, unlock base avatars, and customize a character with owned cosmetics.

## Current Capabilities

- SQL-backed login with in-memory session tokens and role-aware routing.
- SQL-backed student dashboard with attendance status, coin balance, the current avatar, and a Sunday-through-Saturday assignment calendar.
- Classroom assignment templates in `WeeklyAssignmentTemplates` recur by weekday; the dashboard derives their due dates for the current server-local week on each request. This is proof-of-concept mock data, not an assignment editing or completion workflow.
- Student shop with SQL-backed catalog, ownership, coin validation, and atomic purchases.
- Avatar customization with Gerald as the free base, purchased character and cosmetic unlocks, layered visual preview, and SQL-backed saves.
- Student pages include persistent light/dark controls, free background colors, unlocked special background themes, and a coin shop where every base avatar except Gerald costs 10 coins.
- Gerald is the free default avatar. Locked characters remain visible on the avatar page but cannot be equipped until purchased; hats, clothing, accessories, and effects use slot- and character-aware placement.
- Manual coin adjustments are stored in `ManualCoinAdjustments` without creating transaction records.
- The admin dashboard, User Settings, Add Student, Add Teacher, and classroom create/edit flows use PostgreSQL. `ClassroomMemberships` is the normalized roster source after `Seed_DataBase3.sql`; compatibility writes continue maintaining the legacy classroom columns and tables.
- Teacher and admin dashboard scaffolding plus classroom management routes.
- Provider-neutral roster-source and attendance-destination contracts, encrypted connection persistence, external identity mappings, and versioned attendance-export records.
- Admin-only Canvas OAuth and manual roster import for selected courses. Imports include classes, teachers, students, and memberships only; assignments, grades, submissions, course content, passwords, and Canvas student pages are excluded.
- Canvas imports automatically reuse stored external/SIS mappings. Email and local-ID candidates require confirmation, names are never used for matching, new users receive pending local accounts, and removed imported memberships are archived without deleting users or attendance history.

Some teacher/admin reporting and schedule-management flows are still in progress;
see `todo.md` for the remaining project checklist.

## Codebase Map

- `cmd/webserver/main.go` starts the HTTP server on `localhost:4000`.
- `internal/web` contains routes, handlers, in-memory session helpers, and server-rendered student/admin flows.
- `internal/store` contains all PostgreSQL data access, including atomic attendance rewards and shop purchases.
- `internal/domain` contains persisted application models.
- `internal/integrations` contains provider-neutral contracts, capability metadata, provider registration, and AES-GCM credential encryption.
- `internal/integrations/canvas` contains the Canvas OAuth client and roster-only adapter.
- `internal/view` contains embedded templates, static CSS, and images.

PostgreSQL is the application's only runtime data store. The browser cookie contains an opaque token; its short-lived session record remains in application memory and references the SQL `Users.UserID`.

## Local Development

Copy the local environment template before starting PostgreSQL or the app:

```powershell
Copy-Item .env.example .env
```
You should change the password to whatever password you want to connect with. 
The checked-in `.env.example` connects a Go process running on the host to
PostgreSQL through `localhost:5433`. The application loads `.env` when present,
but it does not replace environment variables that are already set.

Run automated validation with:

```sh
go test ./...
```

To run the app manually:

```sh
go run ./cmd/webserver
```

To manually add or subtract coins, insert or update the student's amount in
`ManualCoinAdjustments`. That amount is added to the starting balance and
the sum of `Transactions`.

Added CodeQL


## Database setup

1. Start the PostgreSQL container. The Compose environment creates the
   `attendancehackday` database and runs `init.sql` automatically on the first
   start of a new database volume:

```powershell
docker-compose up -d
```

2. Verify PostgreSQL and the application database are ready:

```powershell
docker-compose exec db pg_isready -U attendance -d attendancehackday
```

The application defaults to
`postgres://attendance:Password123!@localhost:5433/attendancehackday?sslmode=disable`.
For a hosted deployment, configure `DATABASE_URL` in the hosting provider with
the complete managed PostgreSQL connection string. That injected value takes
precedence over `.env`; do not deploy the local `.env` file. If the application
is added to this Compose network later, use `db:5432` as its database host and
port instead of `localhost:5433`.

So basically, we will set up the database with supabase. They will give the database URL to us, which we will store in a secrate manager. Then when we host it, we will configure the environement variables to point to our secrate.

`INTEGRATION_CREDENTIAL_KEY` optionally enables encrypted provider credential
storage. Set it to a base64-encoded 32-byte AES-256 key. The main application
continues to start when the value is absent or invalid, but integration
credential reads and writes remain disabled. Do not change or discard a key
after credentials have been stored unless a future key-rotation process has
re-encrypted those records.

Canvas roster import additionally uses:

- `CANVAS_CLIENT_ID`: the Canvas OAuth developer-key client ID.
- `CANVAS_CLIENT_SECRET`: the matching developer-key secret.
- `CANVAS_REDIRECT_URL`: the exact registered callback URL, normally
  `http://localhost:4000/admin/integrations/canvas/callback` for local work.

The Canvas URL and account ID are entered by an authorized Attendance Quest
administrator. Access and refresh tokens are stored only through the encrypted
integration credential boundary. Course selections are non-secret connection
configuration. The admin manually previews and confirms every initial import
and later synchronization from **Admin → Canvas Import**.


## Check Database in DBeaver

1. Open a new connection

2. Select PostgreSQL

3. Fill in the Info
 Host: localhost
 Port: 5433
 Database: attendancehackday
 User name: attendance
 Password: Password123!

4. Click test connection. If it passes then click finish


## Seed the DataBase

1. Make sure the database is up

    ```powershell
    docker-compose up -d
    ```

2. Seed the base data with the following command.

    ```powershell
    Get-Content -Raw .\Seed_DataBase.sql | docker-compose exec -T db psql --set=ON_ERROR_STOP=1 --username=attendance --dbname=attendancehackday
    ```

    Bash / WSL:

    ```bash
    docker-compose exec -T db psql --set=ON_ERROR_STOP=1 --username=attendance --dbname=attendancehackday < Seed_DataBase.sql
    ```

3. Apply the idempotent delta seed for the latest student records, image path metadata, and recurring weekly assignment templates. This includes the final records migrated from the retired JSON store.

    ```powershell
    Get-Content -Raw .\Seed_DataBase2.sql | docker-compose exec -T db psql --set=ON_ERROR_STOP=1 --username=attendance --dbname=attendancehackday
    ```

    Bash / WSL:

    ```bash
    docker-compose exec -T db psql --set=ON_ERROR_STOP=1 --username=attendance --dbname=attendancehackday < Seed_DataBase2.sql
    ```

4. Apply the additive integration foundation migration before deploying code
   that reads `ClassroomMemberships`. It backfills existing classroom and
   attendance data while retaining `Users.ClassroomID`, `Classrooms.TeacherID`,
   `ClassroomStudents`, and `AttendanceRecords` for compatibility.

    ```powershell
    Get-Content -Raw .\Seed_DataBase3.sql | docker exec -i attendance-postgres psql --set=ON_ERROR_STOP=1 --username=attendance --dbname=attendancehackday
    ```

    Bash / WSL:

    ```bash
    docker exec -i attendance-postgres psql --set=ON_ERROR_STOP=1 --username=attendance --dbname=attendancehackday < Seed_DataBase3.sql
    ```

The application does not apply `Seed_DataBase3.sql` automatically. It must be
applied before using Canvas roster import. Part 2 uses the existing Part 1
tables and does not require an additional migration. Attendance export remains
unimplemented.
