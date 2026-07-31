package prizeemail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

var ErrSMTPDisabled = errors.New("SMTP is not configured")

type SMTPConfig struct {
	Host       string
	Port       string
	Username   string
	Password   string
	From       string
	RequireTLS bool
}

type SMTPMailer struct{ config SMTPConfig }

// SMTPConfigFromEnvironment keeps credentials out of application state and
// requires encrypted transport by default for every non-local SMTP server.
func SMTPConfigFromEnvironment() (SMTPConfig, error) {
	config := SMTPConfig{
		Host: strings.TrimSpace(os.Getenv("SMTP_HOST")), Port: strings.TrimSpace(os.Getenv("SMTP_PORT")),
		Username: strings.TrimSpace(os.Getenv("SMTP_USERNAME")), Password: os.Getenv("SMTP_PASSWORD"),
		From: strings.TrimSpace(os.Getenv("SMTP_FROM")), RequireTLS: true,
	}
	if config.Port == "" {
		config.Port = "587"
	}
	if value := strings.TrimSpace(os.Getenv("SMTP_REQUIRE_TLS")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return config, fmt.Errorf("SMTP_REQUIRE_TLS: %w", err)
		}
		config.RequireTLS = parsed
	}
	if config.Host == "" || config.From == "" {
		return config, ErrSMTPDisabled
	}
	if _, err := mail.ParseAddress(config.From); err != nil {
		return config, fmt.Errorf("SMTP_FROM: %w", err)
	}
	if (config.Username == "") != (config.Password == "") {
		return config, errors.New("SMTP_USERNAME and SMTP_PASSWORD must be configured together")
	}
	host := strings.ToLower(config.Host)
	if !config.RequireTLS && host != "localhost" && host != "127.0.0.1" {
		return config, errors.New("plaintext SMTP is allowed only for localhost")
	}
	return config, nil
}

func NewSMTPMailer(config SMTPConfig) *SMTPMailer { return &SMTPMailer{config: config} }

func (m *SMTPMailer) Send(ctx context.Context, message Message) error {
	if m == nil {
		return ErrSMTPDisabled
	}
	from, err := mail.ParseAddress(m.config.From)
	if err != nil {
		return fmt.Errorf("invalid SMTP_FROM address: %w", err)
	}
	to, err := mail.ParseAddress(message.To)
	if err != nil {
		return fmt.Errorf("invalid recipient address: %w", err)
	}
	address := net.JoinHostPort(m.config.Host, m.config.Port)
	connection, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connect SMTP: %w", err)
	}
	defer connection.Close()
	client, err := smtp.NewClient(connection, m.config.Host)
	if err != nil {
		return fmt.Errorf("start SMTP client: %w", err)
	}
	defer client.Close()
	if m.config.RequireTLS {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return errors.New("SMTP server does not support required STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: m.config.Host}); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
	}
	if m.config.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.config.Username, m.config.Password, m.config.Host)); err != nil {
			return fmt.Errorf("authenticate SMTP: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	subject := strings.NewReplacer("\r", " ", "\n", " ").Replace(message.Subject)
	body := fmt.Sprintf("To: %s\r\nFrom: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", to.String(), from.String(), mime.QEncoding.Encode("UTF-8", subject), message.Body)
	if _, err := w.Write([]byte(body)); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
