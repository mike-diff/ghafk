// Package ghapp mints GitHub App installation tokens so ghafk can act as its own bot account.
package ghapp

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var githubAPI = "https://api.github.com"

// Identity is an installation token and the app login it acts as.
type Identity struct {
	Token string
	Login string
}

// SecretMode reports whether a secret file is private: readable only by its owner,
// or owned by root and readable by its group but writable only by root.
func SecretMode(info os.FileInfo) bool {
	perm := info.Mode().Perm()
	if perm&0o077 == 0 {
		return true
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == 0 && perm&0o037 == 0
}

// LoadConfig reads the GitHub App ID from dir/app and its private key from dir/app.pem.
// A missing ID file means no app is configured; the key must be mode 0600.
func LoadConfig(dir string) (string, *rsa.PrivateKey, error) {
	data, err := os.ReadFile(filepath.Join(dir, "app"))
	if os.IsNotExist(err) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", nil, nil
	}
	path := filepath.Join(dir, "app.pem")
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}
	if !SecretMode(info) {
		return "", nil, fmt.Errorf("%s: key file mode is %o, want 0600 or 0400", path, info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", nil, fmt.Errorf("%s: no PEM block", path)
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return id, key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", path, err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return "", nil, fmt.Errorf("%s: not an RSA key", path)
	}
	return id, key, nil
}

func signJWT(id string, key *rsa.PrivateKey, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	claims, err := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": id})
	if err != nil {
		return "", err
	}
	signed := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signed + "." + enc.EncodeToString(sig), nil
}

func githubCall(method, path, jwt string, in, out any) error {
	var payload io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, githubAPI+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.SplitN(string(body), "\n", 2)[0])
	}
	return json.Unmarshal(body, out)
}

// Mint exchanges a signed app JWT for an installation token that covers only repo
// ("owner/name") with issue and pull request write access and read access to contents.
func Mint(id string, key *rsa.PrivateKey, repo string, now time.Time) (Identity, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return Identity{}, fmt.Errorf("bad repository %q", repo)
	}
	jwt, err := signJWT(id, key, now)
	if err != nil {
		return Identity{}, err
	}
	var app struct {
		Slug string `json:"slug"`
	}
	if err := githubCall("GET", "/app", jwt, nil, &app); err != nil {
		return Identity{}, err
	}
	var install struct {
		ID int64 `json:"id"`
	}
	if err := githubCall("GET", "/repos/"+owner+"/"+name+"/installation", jwt, nil, &install); err != nil {
		return Identity{}, fmt.Errorf("app %s is not installed on %s: %w", id, repo, err)
	}
	scope := map[string]any{
		"repositories": []string{name},
		"permissions":  map[string]string{"contents": "read", "issues": "write", "pull_requests": "write", "metadata": "read"},
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := githubCall("POST", fmt.Sprintf("/app/installations/%d/access_tokens", install.ID), jwt, scope, &tok); err != nil {
		return Identity{}, err
	}
	if tok.Token == "" || app.Slug == "" {
		return Identity{}, errors.New("GitHub returned no installation token or app name")
	}
	return Identity{Token: tok.Token, Login: app.Slug}, nil
}

// ErrNoApp reports that dir holds no app ID, so the caller acts as the owner.
var ErrNoApp = errors.New("no app configured")

// Minter returns a function that loads the app from dir and mints a token for one repository.
func Minter(dir string) func(repo string) (Identity, error) {
	return func(repo string) (Identity, error) {
		id, key, err := LoadConfig(dir)
		if err != nil {
			return Identity{}, err
		}
		if id == "" {
			return Identity{}, ErrNoApp
		}
		return Mint(id, key, repo, time.Now())
	}
}
