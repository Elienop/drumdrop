package musora

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/elienop/drumdrop/internal/config"
	"github.com/elienop/drumdrop/internal/secrets"
)

// AuthBase is a var (not const) so tests can point it at httptest.
var AuthBase = "https://api.musora.com/api/user-management-system/v1"

const sessionCookieName = "musora_platform_backend_session"

func writeFile0600(path, data string) error {
	if err := os.MkdirAll(config.ConfigDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0o600)
}

func LoadCookie() string {
	b, err := os.ReadFile(config.CookiePath())
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(b))
}

func SaveCreds(email, password string) error {
	blob, err := secrets.Encrypt(fmt.Sprintf("%s\x00%s", email, password))
	if err != nil {
		return err
	}
	return writeFile0600(config.CredsPath(), blob)
}

func LoadCreds() (email, password string, ok bool) {
	b, err := os.ReadFile(config.CredsPath())
	if err != nil {
		return "", "", false
	}
	dec, err := secrets.Decrypt(string(b))
	if err != nil {
		return "", "", false
	}
	parts := bytes.SplitN([]byte(dec), []byte{0}, 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return string(parts[0]), string(parts[1]), true
}

// ErrLoginRejected is what Login's error wraps when Musora answered and refused
// the email and password: any 4xx but 408 (timeout) and 429 (too many tries),
// which say nothing about the credentials. Its text keeps the CLI's
// "login failed: <Musora's message>".
var ErrLoginRejected = errors.New("login failed")

// ErrSessionNotSaved is what Login's error wraps when Musora accepted the login
// but the session cookie could not be written to the config folder.
var ErrSessionNotSaved = errors.New("login: save the session")

// Login signs in to Musora and saves the session cookie. Its error wraps
// ErrLoginRejected when Musora refused the credentials, ErrSessionNotSaved when
// the cookie could not be written; any other error means Musora could not be
// reached or its answer could not be read (a network error, a 5xx, a 408 or
// 429, a 2xx that does not decode or carries no user or no session cookie).
func Login(email, password string) (cookie string, err error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequest(http.MethodPost, AuthBase+"/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var parsed struct {
		User    map[string]any `json:"user"`
		Message string         `json:"message"`
	}
	// Surface a malformed 2xx body as a decode error rather than a confusing
	// "login failed: 200": a decode failure means we cannot trust the response.
	if decErr := json.NewDecoder(resp.Body).Decode(&parsed); decErr != nil && resp.StatusCode/100 == 2 {
		return "", fmt.Errorf("login: decode response: %w", decErr)
	}
	if resp.StatusCode/100 != 2 || parsed.User == nil {
		msg := parsed.Message
		if msg == "" {
			msg = fmt.Sprintf("%d", resp.StatusCode)
		}
		if rejectsCredentials(resp.StatusCode) {
			return "", fmt.Errorf("%w: %s", ErrLoginRejected, msg)
		}
		return "", fmt.Errorf("login failed: %s", msg)
	}
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			cookie = fmt.Sprintf("%s=%s", c.Name, c.Value)
		}
	}
	if cookie == "" {
		return "", errors.New("login succeeded but no session cookie returned")
	}
	if err := writeFile0600(config.CookiePath(), cookie); err != nil {
		return "", fmt.Errorf("%w: %w", ErrSessionNotSaved, err)
	}
	return cookie, nil
}

// rejectsCredentials reports whether a login answered with status refused the
// credentials themselves (see ErrLoginRejected).
func rejectsCredentials(status int) bool {
	return status/100 == 4 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests
}

// Me returns true if the cookie is accepted by /me (401 -> false).
func Me(cookie string) (bool, error) {
	req, _ := http.NewRequest(http.MethodGet, AuthBase+"/me", nil)
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", browserUA)
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return false, nil
	}
	return resp.StatusCode/100 == 2, nil
}
