package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

func loginCmd(e *env) *cobra.Command {
	var email, server string
	var google bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in and store a personal token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if server != "" {
				e.cfg.Server = server
			}
			if e.cfg.Server == "" {
				return errors.New("--server is required the first time")
			}
			if google && email != "" {
				return errors.New("use only one of --email or --google")
			}
			ctx := cmd.Context()
			var session string
			var err error
			switch {
			case google:
				session, err = googleLogin(ctx, e)
			case email != "":
				session, err = emailLogin(ctx, e, email)
			default:
				return errors.New("use --email you@lab.org or --google")
			}
			if err != nil {
				return err
			}
			host, _ := os.Hostname()
			var tok struct {
				Token string `json:"token"`
			}
			c := &Client{Server: e.cfg.Server, Token: session}
			if err := c.Do(ctx, "POST", "/api/tokens", map[string]string{"name": "CLI (" + host + ")"}, &tok); err != nil {
				return err
			}
			if strings.TrimSpace(tok.Token) == "" {
				return errors.New("server returned an empty personal token")
			}
			e.cfg.Token = tok.Token
			e.cfg.Workspace = ""
			if err := SaveConfig(e.path, e.cfg); err != nil {
				return err
			}
			fmt.Fprintln(e.out, "Signed in.")
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "sign in with a code sent to this email")
	cmd.Flags().BoolVar(&google, "google", false, "sign in with Google in the browser")
	cmd.Flags().StringVar(&server, "server", "", "control service URL")
	return cmd
}

func emailLogin(ctx context.Context, e *env, email string) (string, error) {
	c := &Client{Server: e.cfg.Server}
	if err := c.Do(ctx, "POST", "/auth/send-code", map[string]string{"email": email}, nil); err != nil {
		return "", err
	}
	fmt.Fprint(e.out, "Code sent to "+email+". Enter it: ")
	line, err := bufio.NewReader(e.in).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.Do(ctx, "POST", "/auth/verify-code", map[string]string{"email": email, "code": strings.TrimSpace(line)}, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Token) == "" {
		return "", errors.New("server returned an empty session token")
	}
	return out.Token, nil
}

// googleLogin opens the browser and waits for the server to hand the session
// back to a loopback-only listener.
func googleLogin(ctx context.Context, e *env) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)
	got := make(chan string, 1)
	var accepted atomic.Bool
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		if len(q["token"]) != 1 || len(q["state"]) != 1 || strings.TrimSpace(q.Get("token")) == "" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(nonce)) != 1 {
			http.Error(w, "invalid sign-in callback", http.StatusBadRequest)
			return
		}
		if !accepted.CompareAndSwap(false, true) {
			http.Error(w, "callback already accepted", http.StatusConflict)
			return
		}
		got <- q.Get("token")
		fmt.Fprintln(w, "Signed in. You can close this tab.")
	})}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	defer func() { srv.Close(); <-serveErr }()
	u := strings.TrimRight(e.cfg.Server, "/") + "/auth/google?" + url.Values{"cli_port": {fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)}, "cli_state": {nonce}}.Encode()
	fmt.Fprintln(e.out, "Opening", u)
	opener := e.openBrowser
	if opener == nil {
		opener = openBrowser
	}
	if err := opener(u); err != nil {
		return "", fmt.Errorf("open browser: %w", err)
	}
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	select {
	case tok := <-got:
		return tok, nil
	case <-timer.C:
		return "", errors.New("timed out waiting for the browser")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// openBrowser uses the platform URL launcher and reports launch failures.
func openBrowser(u string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
		args = []string{u}
	case "linux":
		name = "xdg-open"
		args = []string{u}
	case "windows":
		name = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", u}
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func logoutCmd(e *env) *cobra.Command {
	return &cobra.Command{Use: "logout", Short: "Forget the stored token", RunE: func(*cobra.Command, []string) error {
		e.cfg.Token = ""
		return SaveConfig(e.path, e.cfg)
	}}
}

func whoamiCmd(e *env) *cobra.Command {
	return &cobra.Command{Use: "whoami", Short: "Show the signed-in user", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := e.signedIn(); err != nil {
			return err
		}
		var me struct{ Email, Name string }
		if err := e.client().Do(cmd.Context(), "GET", "/api/me", nil, &me); err != nil {
			return err
		}
		fmt.Fprintln(e.out, me.Email, me.Name)
		return nil
	}}
}
