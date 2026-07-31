# Attendance-HackDay

Attendance Quest is a Go server-rendered attendance rewards app for students,
teachers, and admins. Students can log in, mark attendance, earn coins, buy shop
items, unlock base avatars, and customize a character with owned cosmetics.

## Current Capabilities

- SQL-backed login with in-memory session tokens and role-aware routing.
- SQL-backed student dashboard focused on attendance status, rewards, the current avatar, and upcoming double-reward days.
- Student shop with SQL-backed catalog, ownership, coin validation, and atomic purchases.
- Avatar customization with Gerald as the free base, purchased character and cosmetic unlocks, layered visual preview, and SQL-backed saves.
- Student pages include persistent light/dark controls, free background colors, unlocked special background themes, and a coin shop where every base avatar except Gerald costs 10 coins.
- Gerald is the free default avatar. Locked characters remain visible on the avatar page but cannot be equipped until purchased; hats, clothing, accessories, and effects use slot- and character-aware placement.
- Manual coin adjustments are stored in `ManualCoinAdjustments` without creating transaction records.
- The admin dashboard, User Settings, Add Student, Add Teacher, and classroom create/edit flows use PostgreSQL. `ClassroomMemberships` is the normalized roster source after `Seed_DataBase3.sql`; compatibility writes continue maintaining the legacy classroom columns and tables.
- Teacher and admin dashboard scaffolding plus classroom management routes.
- Teacher attendance approval for assigned classes, with admin access to every class. Student check-ins default the daily roster to present, missing check-ins default to absent, and each whole-class approval is stored as an immutable version. A later approval creates a correction-pending version without changing check-in rewards.
- Provider-neutral roster-source and attendance-destination contracts, encrypted connection persistence, external identity mappings, and versioned attendance-export records.
- Provider-specific Canvas, Ed-Fi, encrypted connection, mapping, and export code remains available as dormant architecture. The admin **Roster Connection** and **Official Records** screens are presentation placeholders and their mutation routes are not registered.
- The student navigation includes a static **School Portal** placeholder showing where a future Canvas or provider-neutral school connection could appear. It performs no OAuth, iframe loading, credential lookup, or database migration check.
- Admin pages support persistent light and dark display modes while the functional Attendance review remains available.

Some teacher/admin reporting and schedule-management flows are still in progress;
see `todo.md` for the remaining project checklist.

See [Integration Architecture and Operations](docs/integrations.md) for provider
design, Canvas and Ed-Fi setup, encryption, approval, retry, logging, and adapter
extension guidance.

## Codebase Map

- `cmd/webserver/main.go` starts the HTTP server on `localhost:4000`.
- `internal/web` contains routes, handlers, in-memory session helpers, and server-rendered student/admin flows.
- `internal/store` contains all PostgreSQL data access, including atomic attendance rewards and shop purchases.
- `internal/domain` contains persisted application models.
- `internal/integrations` contains provider-neutral contracts, capability metadata, provider registration, and AES-GCM credential encryption.
- `internal/integrations/canvas` contains the Canvas OAuth client, roster-only adapter, and dormant owner-scoped connection support.
- `internal/integrations/edfi` contains the official daily-attendance destination adapter.
- `internal/attendanceexport` orchestrates destination validation, durable outbox delivery, bounded retries, and provider-neutral response handling.
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

The automated suite covers several layers:

- Unit and component tests exercise avatar rules, provider registration,
  credential encryption, export orchestration, sessions, CSRF, authorization,
  template rendering, and static asset contracts.
- Adapter tests use `httptest` servers to verify outbound Ed-Fi behavior without
  contacting a real provider.
- PostgreSQL integration tests exercise SQL transactions and normalized roster
  reads against a disposable database. Run them by setting `TEST_DATABASE_URL`
  and using `go test -tags=integration -count=1 -v ./internal/store`.
- CI shuffles test order and runs the complete suite under Go's race detector.

## CI/CD Pipeline

GitHub Actions runs `.github/workflows/ci-cd.yml` for every push, for pull
requests targeting `main`, and when started manually. The pipeline uses the Go
version declared in `go.mod`, runs vet plus shuffled tests, runs a separate race
detector job, verifies the store against PostgreSQL 16, and builds the web
server.

After validation succeeds on `main` or on a tag beginning with `v`, the delivery
job builds a stripped Linux AMD64 server binary and uploads a compressed bundle
plus its SHA-256 checksum as a GitHub Actions artifact. Artifacts are retained
for 14 days. The existing CodeQL workflow continues to perform security scans.

This is continuous delivery, not provider-specific deployment: the repository
does not currently identify a production hosting provider. Connect the delivery
job to that provider after choosing the target, and configure `DATABASE_URL` and
any integration credentials through its secret manager.

To run the app manually:

```sh
go run ./cmd/webserver
```

To manually add or subtract coins, insert or update the student's amount in
`ManualCoinAdjustments`. That amount is added to the starting balance and
the sum of `Transactions`.

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

For hosted deployments, store `DATABASE_URL` and integration secrets in the
hosting platform's secret manager.

`INTEGRATION_CREDENTIAL_KEY` enables encrypted provider credentials. Dormant
Canvas adapter code can also use `CANVAS_CLIENT_ID`, `CANVAS_CLIENT_SECRET`, and
`CANVAS_REDIRECT_URL` when its routes are deliberately re-enabled. The current
student and admin connection placeholders require none of these values. See the
[integration runbook](docs/integrations.md) before connecting Canvas or Ed-Fi.

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

3. Apply the idempotent delta seed for the latest student, shop, avatar, and
   image-path records. This includes the final records migrated from the
   retired JSON store.

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
    Get-Content -Raw .\Seed_DataBase3.sql | docker-compose exec -T db psql --set=ON_ERROR_STOP=1 --username=attendance --dbname=attendancehackday
    ```

    Bash / WSL:

    ```bash
    docker-compose exec -T db psql --set=ON_ERROR_STOP=1 --username=attendance --dbname=attendancehackday < Seed_DataBase3.sql
    ```

5. Optionally apply the additive owner-scoping migration before re-enabling the
   dormant student provider-account linking code:

    ```powershell
    Get-Content -Raw .\Seed_DataBase4.sql | docker-compose exec -T db psql --set=ON_ERROR_STOP=1 --username=attendance --dbname=attendancehackday
    ```

    Bash / WSL:

    ```bash
    docker-compose exec -T db psql --set=ON_ERROR_STOP=1 --username=attendance --dbname=attendancehackday < Seed_DataBase4.sql
    ```

The application does not apply `Seed_DataBase3.sql` automatically. It must be
applied before deploying the updated membership, teacher approval, or dormant
provider store code. Approval and provider scaffolding use these integration
tables and require no later migration.
The application also does not apply `Seed_DataBase4.sql`. The current static
student School Portal does not need it; apply it before re-enabling persisted
student provider connections.

The demo seeds use a provider-neutral `Demo Elementary School`. Every seeded
classroom membership references a seeded user with the matching role, and no
Canvas, Ed-Fi, or other external connection is inserted automatically.
