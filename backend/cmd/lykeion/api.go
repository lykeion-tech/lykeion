package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type deviceConfig struct {
	Endpoint   string `json:"endpoint"`
	Credential string `json:"credential"`
}

func validEndpoint(value string) (string, error) {
	u, e := url.Parse(value)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Host == "" {
		return "", errors.New("invalid API origin")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1")) {
		return "", errors.New("HTTPS or loopback HTTP required")
	}
	return u.Scheme + "://" + u.Host, nil
}
func privateRead(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("credential/code file must be a private regular file (0600)")
	}
	return os.ReadFile(path)
}
func apiCall(config deviceConfig, method, path string, body []byte, key string) ([]byte, error) {
	endpoint, err := validEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/v1/") || strings.Contains(u.Path, "..") || u.Fragment != "" {
		return nil, errors.New("a /v1/ API path is required")
	}
	req, err := http.NewRequest(method, endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if config.Credential != "" {
		req.Header.Set("Authorization", "Bearer "+config.Credential)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	client := http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirect refused") }}
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("API unavailable; retry with the same command body and key")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2_000_001))
	if err != nil || len(raw) > 2_000_000 {
		return nil, errors.New("invalid API response")
	}
	if response.StatusCode >= 300 {
		var p struct {
			Code string `json:"code"`
		}
		json.Unmarshal(raw, &p)
		return nil, fmt.Errorf("API %d: %s", response.StatusCode, p.Code)
	}
	return raw, nil
}
func runAPI(argv []string) error {
	args := flag.NewFlagSet(argv[0], flag.ContinueOnError)
	endpoint := args.String("endpoint", "", "Control API origin")
	credentials := args.String("credentials", "", "Private CLI session file")
	codeFile := args.String("code-file", "", "Private installation code file")
	method := args.String("method", "POST", "HTTP method for uploads and invitation acceptance: POST or PUT")
	apiPath := args.String("path", "", "Read path under /v1")
	workspace := args.String("workspace", "", "Workspace ID")
	name := args.String("name", "", "Named command")
	bodyFile := args.String("body-file", "", "Complete version-pinned JSON command envelope")
	key := args.String("key", "", "Stable idempotency key; retain for retries")
	if err := args.Parse(argv[1:]); err != nil {
		return err
	}
	if *credentials == "" || args.NArg() != 0 {
		return errors.New("--credentials is required")
	}
	var config deviceConfig
	if argv[0] == "login" {
		origin, err := validEndpoint(*endpoint)
		if err != nil {
			return err
		}
		code, err := privateRead(*codeFile)
		if err != nil {
			return err
		}
		// Reserve the private destination before consuming the single-use code.
		file, err := os.OpenFile(*credentials, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return errors.New("credentials must name a new private file")
		}
		success := false
		defer func() {
			file.Close()
			if !success {
				os.Remove(*credentials)
			}
		}()
		body, _ := json.Marshal(map[string]string{"code": strings.TrimSpace(string(code))})
		raw, err := apiCall(deviceConfig{Endpoint: origin}, "POST", "/v1/auth/device/exchange", body, "")
		if err != nil {
			return err
		}
		var response struct {
			Data struct {
				Credential string `json:"credential"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &response) != nil || response.Data.Credential == "" {
			return errors.New("invalid device session")
		}
		config = deviceConfig{origin, response.Data.Credential}
		encoded, _ := json.Marshal(config)
		if _, err = file.Write(encoded); err == nil {
			err = file.Sync()
		}
		if err != nil {
			return errors.New("credential write failed; obtain a new login code")
		}
		success = true
		fmt.Println("CLI session saved privately; expires in eight hours.")
		return nil
	}
	raw, err := privateRead(*credentials)
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, &config) != nil {
		return errors.New("invalid credentials file")
	}
	if *endpoint != "" && *endpoint != config.Endpoint {
		return errors.New("credentials are bound to their saved origin")
	}
	switch argv[0] {
	case "get":
		raw, err = apiCall(config, "GET", *apiPath, nil, "")
	case "request":
		if (*method != "POST" && *method != "PUT") || strings.HasPrefix(*apiPath, "/v1/auth/") || *key == "" {
			return errors.New("request requires POST/PUT, a stable key and a non-auth API path")
		}
		body, e := os.ReadFile(*bodyFile)
		if e != nil || len(body) > 1_000_000 || !json.Valid(body) {
			return errors.New("valid JSON body file required")
		}
		raw, err = apiCall(config, *method, *apiPath, body, *key)
	case "command":
		if *workspace == "" || *name == "" || *key == "" || strings.ContainsAny(*workspace+*name, "/?#\\") {
			return errors.New("workspace, command name and stable key required")
		}
		body, e := os.ReadFile(*bodyFile)
		if e != nil {
			return errors.New("cannot read command envelope")
		}
		if !json.Valid(body) || len(body) > 1_000_000 {
			return errors.New("invalid command envelope")
		}
		raw, err = apiCall(config, "POST", "/v1/workspaces/"+*workspace+"/commands/"+*name, body, *key)
	case "logout":
		raw, err = apiCall(config, "POST", "/v1/auth/logout", nil, "")
		if err == nil {
			err = os.Remove(*credentials)
		}
	default:
		return errors.New("unknown API action")
	}
	if err != nil {
		return err
	}
	if len(raw) > 0 {
		fmt.Println(string(raw))
	}
	return nil
}
