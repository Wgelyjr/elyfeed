package auth

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
)

// Mailer delivers transactional email (verification links, password resets).
type Mailer interface {
	Send(to, subject, body string) error
}

// SMTPMailer sends email over SMTP. With ImplicitTLS it dials an already-
// encrypted connection (port 465 style); otherwise it uses the default
// path, which performs STARTTLS when the server offers it (port 587 style).
type SMTPMailer struct {
	host        string
	port        int
	user, pass  string
	from        string
	implicitTLS bool
}

// NewSMTPMailer builds an SMTP mailer. user/pass may be empty for servers
// that do not require authentication.
func NewSMTPMailer(host string, port int, user, pass, from string, implicitTLS bool) *SMTPMailer {
	return &SMTPMailer{host: host, port: port, user: user, pass: pass, from: from, implicitTLS: implicitTLS}
}

func (m *SMTPMailer) Send(to, subject, body string) error {
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s",
		m.from, to, subject, body)
	addr := net.JoinHostPort(m.host, strconv.Itoa(m.port))
	envelope := envelopeAddress(m.from)

	if !m.implicitTLS {
		var a smtp.Auth
		if m.user != "" {
			a = smtp.PlainAuth("", m.user, m.pass, m.host)
		}
		if err := smtp.SendMail(addr, a, envelope, []string{to}, []byte(msg)); err != nil {
			return fmt.Errorf("smtp send: %w", err)
		}
		return nil
	}

	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: m.host})
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()
	if m.user != "" {
		if err := client.Auth(smtp.PlainAuth("", m.user, m.pass, m.host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(envelope); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp write close: %w", err)
	}
	return client.Quit()
}

// envelopeAddress extracts the bare SMTP address from a from-address that may
// carry a display name ("Display <addr>" or "<addr>"). The SMTP MAIL FROM
// envelope command requires a bare address; the display form is only valid in
// the From: header. A plain address is returned trimmed and unchanged.
func envelopeAddress(from string) string {
	if a, err := mail.ParseAddress(from); err == nil && a.Address != "" {
		return a.Address
	}
	return strings.TrimSpace(from)
}

// ConsoleMailer prints emails to the log. It is the development fallback
// when no SMTP server is configured, so the verification flow stays
// testable end to end.
type ConsoleMailer struct {
	log *slog.Logger
}

// NewConsoleMailer builds a console mailer.
func NewConsoleMailer(log *slog.Logger) *ConsoleMailer {
	return &ConsoleMailer{log: log}
}

func (m *ConsoleMailer) Send(to, subject, body string) error {
	m.log.Info("email (console mailer)", "to", to, "subject", subject, "body", body)
	return nil
}
