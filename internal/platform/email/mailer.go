package email

import (
	"fmt"
	"net/smtp"
	"strconv"
	"strings"
)

// Mailer sends emails for account and system notifications.
type Mailer interface {
	Send(to, subject, body string) error
}

// NoopMailer discards outbound email messages.
type NoopMailer struct{}

// Send implements the Mailer interface without sending anything.
func (NoopMailer) Send(to, subject, body string) error {
	return nil
}

// SMTPMailer sends email through an SMTP server.
type SMTPMailer struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// Send delivers a plain-text email via SMTP.
func (m SMTPMailer) Send(to, subject, body string) error {
	addr := netJoinHostPort(m.Host, m.Port)
	headers := []string{
		"From: " + m.From,
		"To: " + to,
		"Subject: " + subject,
		"MIME-version: 1.0;",
		"Content-Type: text/plain; charset=utf-8;",
		"",
		body,
	}
	msg := strings.Join(headers, "\r\n")

	var auth smtp.Auth
	if m.Username != "" && m.Password != "" {
		auth = smtp.PlainAuth("", m.Username, m.Password, m.Host)
	}

	return smtp.SendMail(addr, auth, m.From, []string{to}, []byte(msg))
}

func netJoinHostPort(host string, port int) string {
	return fmt.Sprintf("%s:%s", host, strconv.Itoa(port))
}
