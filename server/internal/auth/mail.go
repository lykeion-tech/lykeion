package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
)

type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// LogMailer writes mail to the log; for development and self-hosting without SMTP.
type LogMailer struct{}

func (LogMailer) Send(_ context.Context, to, subject, body string) error {
	slog.Info("mail", "to", to, "subject", subject, "body", body)
	return nil
}

type SMTPMailer struct {
	Addr, From, User, Pass string
}

func (m SMTPMailer) Send(_ context.Context, to, subject, body string) error {
	host, _, err := net.SplitHostPort(m.Addr)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s\r\n", m.From, to, subject, body)
	return smtp.SendMail(m.Addr, smtp.PlainAuth("", m.User, m.Pass, host), m.From, []string{to}, []byte(msg))
}
