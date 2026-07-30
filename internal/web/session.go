package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"
)

type sessionRecord struct {
	UserID    string
	CSRFToken string
	ExpiresAt time.Time
}

var (
	sessionMu    sync.RWMutex
	sessionStore = map[string]sessionRecord{}
)

const sessionDuration = 24 * time.Hour

func createSession(w http.ResponseWriter, userID string) error {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return err
	}
	csrfBytes := make([]byte, 32)
	if _, err := rand.Read(csrfBytes); err != nil {
		return err
	}

	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	csrfToken := base64.RawURLEncoding.EncodeToString(csrfBytes)

	sessionMu.Lock()
	sessionStore[token] = sessionRecord{
		UserID:    userID,
		CSRFToken: csrfToken,
		ExpiresAt: time.Now().Add(sessionDuration),
	}
	sessionMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionDuration.Seconds()),
	})

	return nil
}

func getSessionUserID(r *http.Request) (string, error) {
	cookie, err := r.Cookie("session")
	if err != nil {
		return "", errors.New("no session cookie found")
	}

	sessionMu.RLock()
	sess, ok := sessionStore[cookie.Value]
	sessionMu.RUnlock()

	if !ok {
		return "", errors.New("invalid session")
	}

	if time.Now().After(sess.ExpiresAt) {
		sessionMu.Lock()
		delete(sessionStore, cookie.Value)
		sessionMu.Unlock()
		return "", errors.New("session expired")
	}

	return sess.UserID, nil
}

func getCSRFToken(r *http.Request) (string, error) {
	session, err := getSession(r)
	if err != nil {
		return "", err
	}
	if session.CSRFToken == "" {
		return "", errors.New("session has no CSRF token")
	}
	return session.CSRFToken, nil
}

func validCSRFToken(r *http.Request, submitted string) bool {
	expected, err := getCSRFToken(r)
	if err != nil || len(expected) != len(submitted) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(submitted)) == 1
}

func getSession(r *http.Request) (sessionRecord, error) {
	cookie, err := r.Cookie("session")
	if err != nil {
		return sessionRecord{}, errors.New("no session cookie found")
	}
	sessionMu.RLock()
	session, ok := sessionStore[cookie.Value]
	sessionMu.RUnlock()
	if !ok {
		return sessionRecord{}, errors.New("invalid session")
	}
	if time.Now().After(session.ExpiresAt) {
		sessionMu.Lock()
		delete(sessionStore, cookie.Value)
		sessionMu.Unlock()
		return sessionRecord{}, errors.New("session expired")
	}
	return session, nil
}

func clearSessionUser(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("session"); err == nil {
		sessionMu.Lock()
		delete(sessionStore, cookie.Value)
		sessionMu.Unlock()
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}
