package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSignInSourceFallback(t *testing.T) {
	for _, tt := range []struct {
		name, harness, platform    string
		explicit, file, wantLookup bool
	}{
		{"default darwin Claude", "claude", "darwin", false, false, true},
		{"file wins", "claude", "darwin", false, true, false},
		{"explicit profile stays isolated", "claude", "darwin", true, false, false},
		{"linux", "claude", "linux", false, false, false},
		{"codex", "codex", "darwin", false, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "signin")
			if tt.file {
				os.WriteFile(path, []byte("file"), 0600)
			}
			calls := 0
			got, err := readSignIn(path, tt.harness, tt.platform, tt.explicit, func() ([]byte, error) { calls++; return []byte("keychain"), nil })
			if err != nil || (calls == 1) != tt.wantLookup {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			want := ""
			if tt.file {
				want = "file"
			} else if tt.wantLookup {
				want = "keychain"
			}
			if string(got) != want {
				t.Fatal("wrong credential source")
			}
		})
	}
}

func TestKeychainReadIsBoundedAndSanitized(t *testing.T) {
	for _, name := range []string{"valid", "unavailable", "timeout", "overflow", "invalid"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			got, err := readClaudeKeychain(ctx, func(ctx context.Context, w io.Writer) error {
				switch name {
				case "valid":
					_, err := io.WriteString(w, `{"claudeAiOauth":{"accessToken":"synthetic"}}`+"\n")
					return err
				case "unavailable":
					return errors.New("sensitive stderr must never escape")
				case "timeout":
					<-ctx.Done()
					return ctx.Err()
				case "overflow":
					_, err := io.WriteString(w, strings.Repeat("x", maxCredentialBytes+1))
					return err
				default:
					_, err := io.WriteString(w, "sensitive invalid output")
					return err
				}
			})
			if name == "valid" {
				if err != nil || !strings.Contains(string(got), "synthetic") {
					t.Fatal("valid credential lost")
				}
				return
			}
			if len(got) != 0 {
				t.Fatal("credential returned on failed read")
			}
			if name == "unavailable" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("unsanitized or missing error: %v", err)
			}
		})
	}
}

func TestKeychainCopyCannotBypassLimit(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			payload := `{"x":"` + strings.Repeat("x", maxCredentialBytes-8+extra) + `"}`
			got, err := readClaudeKeychain(context.Background(), func(_ context.Context, w io.Writer) error {
				_, err := io.Copy(w, struct{ io.Reader }{strings.NewReader(payload)})
				return err
			})
			if extra == 0 {
				if err != nil || len(got) != maxCredentialBytes {
					t.Fatalf("exact limit: len=%d err=%v", len(got), err)
				}
			} else if len(got) != 0 || err == nil || !strings.Contains(err.Error(), "size limit") {
				t.Fatalf("copy bypassed limit: len=%d err=%v", len(got), err)
			}
		})
	}
}

func TestKeychainSyntheticCommandStdoutLimit(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			bin := script(t, fmt.Sprintf(`printf '{"x":"'; head -c %d /dev/zero | tr '\000' x; printf '"}'`, maxCredentialBytes-8+extra))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			got, err := readClaudeKeychain(ctx, func(ctx context.Context, w io.Writer) error {
				cmd := exec.CommandContext(ctx, bin)
				cmd.Stdout = w
				cmd.Stderr = io.Discard
				cmd.WaitDelay = time.Second
				return cmd.Run()
			})
			if extra == 0 {
				if err != nil || len(got) != maxCredentialBytes {
					t.Fatalf("exact stdout limit: len=%d err=%v", len(got), err)
				}
			} else if len(got) != 0 || err == nil || !strings.Contains(err.Error(), "size limit") {
				t.Fatalf("stdout bypassed limit: len=%d err=%v", len(got), err)
			}
		})
	}
}
