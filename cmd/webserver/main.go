package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"

	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations"
	"github.com/PeterGrunig/Attendance-HackDay/internal/integrations/canvas"
	"github.com/PeterGrunig/Attendance-HackDay/internal/store"
	"github.com/PeterGrunig/Attendance-HackDay/internal/web"
	"github.com/joho/godotenv"

	_ "github.com/lib/pq"
)

func main() {
	_ = godotenv.Load()
	db, err := sql.Open("postgres", databaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	storeOptions := []store.SQLStoreOption{}
	credentialCipher, err := integrations.NewAESGCMCredentialCipher(os.Getenv("INTEGRATION_CREDENTIAL_KEY"))
	if err != nil {
		log.Printf("integration credential storage disabled: %v", err)
	} else {
		storeOptions = append(storeOptions, store.WithCredentialCipher(credentialCipher))
	}

	canvasClient := canvas.New(
		os.Getenv("CANVAS_CLIENT_ID"),
		os.Getenv("CANVAS_CLIENT_SECRET"),
		os.Getenv("CANVAS_REDIRECT_URL"),
	)
	registry := integrations.NewProviderRegistry()
	if err := registry.Register(canvasClient); err != nil {
		log.Printf("Canvas provider registration failed: %v", err)
	}
	web.ConfigureCanvas(canvasClient)

	port := os.Getenv("PORT")
	if port == "" {
		port = "4000"
	}
	log.Printf("starting server on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, web.NewRouter(store.NewSQLStore(db, storeOptions...))))
}

func databaseURL() string {
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value
	}
	return "postgres://attendance:Password123!@localhost:5433/attendancehackday?sslmode=disable"
}
