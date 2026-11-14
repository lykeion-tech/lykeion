package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestSMTPSilentPeerTimeoutAndCancellation(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "cancel"}[cancelEarly], func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				c, err := ln.Accept()
				if err == nil {
					accepted <- c
				}
			}()
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- (SMTPMailer{Addr: ln.Addr().String(), From: "owner@lab.org"}).Send(ctx, "guest@lab.org", "test", "test")
			}()
			var conn net.Conn
			select {
			case conn = <-accepted:
			case <-time.After(time.Second):
				t.Fatal("SMTP not connected")
			}
			defer conn.Close()
			if cancelEarly {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
					t.Fatalf("want context error, got %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("silent SMTP ignored timeout/cancellation")
			}
			conn.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := conn.Read(make([]byte, 1)); err == nil {
				t.Fatal("connection still open")
			} else if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatal("connection retained")
			}
		})
	}
}

func TestSMTPAdvertisedAuthIsRequiredAndUsed(t *testing.T) {
	for _, test := range []struct {
		name                             string
		advertise, authenticate, success bool
	}{
		{"refuse-without-auth", false, true, false},
		{"authenticated-delivery", true, true, true},
		{"unauthenticated-delivery", false, false, true},
	} {
		advertise := test.advertise
		t.Run(test.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			served := make(chan error, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					served <- err
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				wire := textproto.NewConn(conn)
				defer wire.Close()
				if err := wire.PrintfLine("220 localhost ESMTP"); err != nil {
					served <- err
					return
				}
				line, err := wire.ReadLine()
				if err != nil {
					served <- err
					return
				}
				if !strings.HasPrefix(line, "EHLO ") {
					served <- fmt.Errorf("want EHLO, got %q", line)
					return
				}
				if advertise {
					wire.PrintfLine("250-localhost")
					wire.PrintfLine("250 AUTH PLAIN")
				} else {
					wire.PrintfLine("250 localhost")
				}
				line, err = wire.ReadLine()
				if !advertise && err != nil {
					served <- nil
					return
				}
				if advertise {
					if line != "AUTH PLAIN "+base64.StdEncoding.EncodeToString([]byte("\x00user\x00pass")) {
						served <- fmt.Errorf("missing configured authentication: %q (%v)", line, err)
						return
					}
					wire.PrintfLine("235 authenticated")
					line, err = wire.ReadLine()
				}
				if err != nil || line != "MAIL FROM:<owner@lab.org>" {
					served <- fmt.Errorf("MAIL: %q %v", line, err)
					return
				}
				wire.PrintfLine("250 OK")
				line, err = wire.ReadLine()
				if err != nil || line != "RCPT TO:<guest@lab.org>" {
					served <- fmt.Errorf("RCPT: %q %v", line, err)
					return
				}
				wire.PrintfLine("250 OK")
				line, err = wire.ReadLine()
				if err != nil || line != "DATA" {
					served <- fmt.Errorf("DATA: %q %v", line, err)
					return
				}
				wire.PrintfLine("354 continue")
				data, err := io.ReadAll(wire.DotReader())
				if err != nil || !strings.Contains(string(data), "Subject: test\n\nbody\n") {
					served <- fmt.Errorf("mail body %q: %v", data, err)
					return
				}
				wire.PrintfLine("250 queued")
				line, err = wire.ReadLine()
				if err != nil || line != "QUIT" {
					served <- fmt.Errorf("QUIT: %q %v", line, err)
					return
				}
				wire.PrintfLine("221 bye")
				served <- nil
			}()
			mailer := SMTPMailer{Addr: ln.Addr().String(), From: "owner@lab.org"}
			if test.authenticate {
				mailer.User, mailer.Pass = "user", "pass"
			}
			err = mailer.Send(t.Context(), "guest@lab.org", "test", "body")
			if test.success && err != nil {
				t.Fatal(err)
			}
			if !test.success && err == nil {
				t.Error("sent mail without advertised authentication")
			}
			select {
			case err := <-served:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("SMTP fixture stalled")
			}
		})
	}
}
