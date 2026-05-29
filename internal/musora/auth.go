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
	json.NewDecoder(resp.Body).Decode(&parsed)
	if resp.StatusCode/100 != 2 || parsed.User == nil {
		msg := parsed.Message
		if msg == "" {
			msg = fmt.Sprintf("%d", resp.StatusCode)
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
		return "", err
	}
	return cookie, nil
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

func EnsureSession() (string, error) {
	if c := LoadCookie(); c != "" {
		if ok, _ := Me(c); ok {
			return c, nil
		}
	}
	email, password, ok := LoadCreds()
	if !ok {
		return "", errors.New("not logged in — run `drumdrop login` first")
	}
	return Login(email, password)
}
