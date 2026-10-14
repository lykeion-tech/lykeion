package auth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"time"
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

// Send bounds the whole SMTP conversation, including the greeting and TLS
// handshake. Closing the connection on cancellation interrupts blocked I/O.
func (m SMTPMailer) Send(ctx context.Context, to, subject, body string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	defer func() {
		if err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			} else if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
				err = context.DeadlineExceeded
			}
		}
	}()
	host, _, err := net.SplitHostPort(m.Addr)
	if err != nil {
		return err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", m.Addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Hello("localhost"); err != nil {
		return err
	}
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if m.User != "" || m.Pass != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("smtp: server doesn't support AUTH")
		}
		if err := client.Auth(smtp.PlainAuth("", m.User, m.Pass, host)); err != nil {
			return err
		}
	}
	if err := client.Mail(m.From); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s\r\n", m.From, to, subject, body)
	if _, err := writer.Write([]byte(msg)); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
