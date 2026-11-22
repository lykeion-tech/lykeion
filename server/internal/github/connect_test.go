package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStateRoundTrip(t *testing.T) {
	now := time.Unix(1800000000, 0)
	s := &Service{StateSecret: []byte("state signing key"), Config: Config{Slug: "lab"}, now: func() time.Time { return now }}
	ws, user := uuid.New(), uuid.New()
	raw, err := s.InstallURL(ws, user, "")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path != "/apps/lab/installations/new" {
		t.Fatal(raw, err)
	}
	state := u.Query().Get("state")
	id, err := s.verifyState(state)
	if err != nil || id.workspace != ws || id.user != user || id.installation != 0 {
		t.Fatal(id, err)
	}
	if _, err := s.verifyState("x" + state); err == nil {
		t.Fatal("accepted tampering")
	}
	now = now.Add(10 * time.Minute)
	if _, err := s.verifyState(state); err == nil {
		t.Fatal("accepted expired state")
	}
}

func TestOwnershipPersonal(t *testing.T) {
	for _, tc := range []struct {
		user int
		want string
	}{{8, ""}, {7, "You don't control this GitHub installation. Ask an owner or admin of the account to connect it."}} {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/user":
				fmt.Fprintf(w, `{"id":%d}`, tc.user)
			case "/app/installations/1":
				fmt.Fprint(w, `{"account":{"id":8,"type":"User","login":"lab"}}`)
			default:
				t.Errorf("unexpected %s", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(api.Close)
		s := Service{Config: Config{APIURL: api.URL, AppID: 1, PrivateKey: testAppKey(t)}}
		_, err := s.ownedAccount(t.Context(), "user-token", 1)
		checkOwnership(t, err, tc.want)
	}
}

func checkOwnership(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	var ue *UserError
	if !errors.As(err, &ue) || ue.Msg != want {
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestOwnershipOrganization(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		thirdAdmin, empty, incomplete, cycle bool
		want                                 string
	}{
		{name: "all pages", thirdAdmin: true}, {name: "missing admin on second page", want: "You don't control this GitHub installation. Ask an owner or admin of the account to connect it."},
		{name: "empty", empty: true, want: "Select at least one repository on GitHub, then connect again."}, {name: "truncated", thirdAdmin: true, incomplete: true}, {name: "cycle", thirdAdmin: true, cycle: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/user":
					fmt.Fprint(w, `{"id":8}`)
				case "/app/installations/1":
					fmt.Fprint(w, `{"account":{"id":10,"type":"Organization","login":"lab"}}`)
				case "/app/installations/1/access_tokens":
					fmt.Fprint(w, `{"token":"installation-token"}`)
				case "/user/installations/1/repositories", "/installation/repositories":
					userList := r.URL.Path != "/installation/repositories"
					want := "Bearer installation-token"
					if userList {
						want = "Bearer user-token"
					}
					if r.Header.Get("Authorization") != want {
						t.Error("wrong repository token")
					}
					total := 3
					ids := []int{1, 2}
					if tc.empty {
						total = 0
						ids = nil
					} else if r.URL.Query().Get("page") == "" {
						w.Header().Set("Link", `<?page=2>; rel="next"`)
					} else {
						ids = []int{3}
						if tc.cycle {
							w.Header().Set("Link", `<?page=1>; rel="next"`)
							if r.URL.Query().Get("page") == "1" {
								ids = nil
								w.Header().Set("Link", `<?page=2>; rel="next"`)
							}
						}
					}
					if tc.incomplete && !userList {
						total = 4
					}
					repos := []map[string]any{}
					for _, id := range ids {
						repos = append(repos, map[string]any{"id": id, "permissions": map[string]bool{"admin": id < 3 || tc.thirdAdmin}})
					}
					json.NewEncoder(w).Encode(map[string]any{"total_count": total, "repositories": repos})
				default:
					http.NotFound(w, r)
				}
			}))
			defer api.Close()
			s := Service{Config: Config{APIURL: api.URL, AppID: 1, PrivateKey: testAppKey(t)}}
			_, err := s.ownedAccount(t.Context(), "user-token", 1)
			if tc.cycle {
				if err == nil || err.Error() != "GitHub repository pagination repeated a page" {
					t.Fatalf("cycle: %v", err)
				}
				return
			}
			if tc.incomplete {
				if err == nil {
					t.Fatal("accepted incomplete enumeration")
				}
				return
			}
			checkOwnership(t, err, tc.want)
		})
	}
}

func TestOwnershipRejectsSuspendedInstallation(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"id":8}`)
		case "/app/installations/1":
			fmt.Fprint(w, `{"account":{"id":8,"type":"User","login":"lab"},"suspended_at":"2025-01-01T00:00:00Z"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	s := Service{Config: Config{APIURL: api.URL, AppID: 1, PrivateKey: testAppKey(t)}}
	_, err := s.ownedAccount(t.Context(), "user-token", 1)
	checkOwnership(t, err, "This GitHub installation is suspended. Unsuspend it on GitHub, then connect again.")
}
