package ghapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestAppJWTIsSignedWithTheKeyAndNamesTheApp(t *testing.T) {
	key := testKey(t)
	now := time.Unix(1_800_000_000, 0)
	jwt, err := signJWT("12345", key, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts, want 3", len(parts))
	}
	var header struct{ Alg string }
	var claims struct {
		Iss      string
		Iat, Exp int64
	}
	for i, v := range []any{&header, &claims} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatal(err)
		}
	}
	if header.Alg != "RS256" || claims.Iss != "12345" {
		t.Fatalf("header alg %q, iss %q, want RS256 and the app id", header.Alg, claims.Iss)
	}
	if claims.Iat > now.Unix() || claims.Exp <= now.Unix() || claims.Exp > now.Add(10*time.Minute).Unix() {
		t.Fatalf("iat %d, exp %d around now %d: GitHub refuses a future iat or an expiry past ten minutes", claims.Iat, claims.Exp, now.Unix())
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature does not verify with the app key: %v", err)
	}
}

type tokenRequest struct {
	Repositories []string          `json:"repositories"`
	Permissions  map[string]string `json:"permissions"`
}

func fakeGitHubAPI(t *testing.T, installed map[string]int) *tokenRequest {
	t.Helper()
	seen := &tokenRequest{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, "no jwt", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"slug":"ghafk"}`))
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", func(w http.ResponseWriter, r *http.Request) {
		id, ok := installed[r.PathValue("owner")+"/"+r.PathValue("repo")]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		fmt.Fprintf(w, `{"id":%d}`, id)
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(seen); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"token":"install-` + r.PathValue("id") + `"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := githubAPI
	githubAPI = srv.URL
	t.Cleanup(func() { githubAPI = old })
	return seen
}

func TestMintScopesTheTokenToOneRepository(t *testing.T) {
	seen := fakeGitHubAPI(t, map[string]int{"acme/tool": 7, "owner/other": 9})
	id, err := Mint("12345", testKey(t), "acme/tool", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if id.Token != "install-7" || id.Login != "ghafk" {
		t.Fatalf("got token %q as %q, want the installation covering acme/tool", id.Token, id.Login)
	}
	if len(seen.Repositories) != 1 || seen.Repositories[0] != "tool" {
		t.Fatalf("token covers %v; it must cover only the repository being worked", seen.Repositories)
	}
	if seen.Permissions["contents"] != "read" || seen.Permissions["issues"] != "write" || seen.Permissions["pull_requests"] != "write" {
		t.Fatalf("token permissions %v; want contents read, issues and pull requests write", seen.Permissions)
	}
}

func TestMintFailsWhenTheAppIsNotInstalledOnTheRepository(t *testing.T) {
	fakeGitHubAPI(t, map[string]int{"owner/other": 9})
	if _, err := Mint("12345", testKey(t), "acme/tool", time.Now()); err == nil {
		t.Fatal("a token was minted for a repository the app is not installed on")
	}
}

func writeAppConfig(t *testing.T, keyMode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	der := x509.MarshalPKCS1PrivateKey(testKey(t))
	if err := os.WriteFile(filepath.Join(dir, "app"), []byte("12345\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.pem"), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}), keyMode); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadAppConfigReadsTheIDAndKey(t *testing.T) {
	id, key, err := LoadConfig(writeAppConfig(t, 0o600))
	if err != nil || id != "12345" || key == nil {
		t.Fatalf("id %q, key present %v, err %v", id, key != nil, err)
	}
}

func TestLoadAppConfigAbsentMeansNoApp(t *testing.T) {
	id, key, err := LoadConfig(t.TempDir())
	if err != nil || id != "" || key != nil {
		t.Fatalf("no app files must mean no app and no error, got id %q, err %v", id, err)
	}
}

func TestLoadAppConfigRefusesAReadableKey(t *testing.T) {
	if _, _, err := LoadConfig(writeAppConfig(t, 0o640)); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("a key other users can read must be refused, got %v", err)
	}
	if _, _, err := LoadConfig(writeAppConfig(t, 0o400)); err != nil {
		t.Fatalf("a read-only key for its owner must be accepted, got %v", err)
	}
}
