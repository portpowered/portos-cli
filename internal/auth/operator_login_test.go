package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type tokenServer struct {
	*httptest.Server
	forms []url.Values
	// mints records each generateIdToken call: its path, Authorization header and JSON body.
	mints   []mintCall
	mintErr int
}

type mintCall struct {
	path, authorization string
	body                map[string]any
}

func newTokenServer(t *testing.T) *tokenServer {
	t.Helper()
	ts := &tokenServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":generateIdToken") && r.Method == http.MethodPost {
			call := mintCall{path: r.URL.Path, authorization: r.Header.Get("Authorization")}
			_ = json.NewDecoder(r.Body).Decode(&call.body)
			ts.mints = append(ts.mints, call)
			if ts.mintErr != 0 {
				w.WriteHeader(ts.mintErr)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "google-id-token-value"})
			return
		}
		if r.URL.Path != "/auth/token" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		ts.forms = append(ts.forms, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "operational-token", "token_type": "Bearer", "expires_in": 900})
	}))
	t.Cleanup(ts.Close)
	return ts
}

func writeKey(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClientCredentialsSignsCorrectAssertion(t *testing.T) {
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cases := map[string]struct {
		key      any
		alg      string
		wantAlg  string
		pub      any
		keyID    string
		wantFail bool
	}{
		"rsa default":  {key: rsaKey, wantAlg: "RS256", pub: &rsaKey.PublicKey, keyID: "k1"},
		"ec default":   {key: ecKey, wantAlg: "ES256", pub: &ecKey.PublicKey},
		"ec explicit":  {key: ecKey, alg: "ES256", wantAlg: "ES256", pub: &ecKey.PublicKey},
		"alg mismatch": {key: rsaKey, alg: "ES256", wantFail: true},
		"unsupported":  {key: rsaKey, alg: "HS256", wantFail: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newTokenServer(t)
			storage := &mockTokenStorage{}
			svc := NewOperatorLogin(srv.Client(), storage, nil)
			err := svc.ClientCredentials(context.Background(), ClientCredentialsParams{
				AuthURL: srv.URL, ClientID: "port1/systems/zero/oauth-clients/op", KeyFile: writeKey(t, tc.key), Alg: tc.alg, KeyID: tc.keyID,
			})
			if tc.wantFail {
				if err == nil || len(srv.forms) != 0 {
					t.Fatalf("expected a failure before any request; err=%v requests=%d", err, len(srv.forms))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			form := srv.forms[0]
			if form.Get("grant_type") != "client_credentials" || form.Get("client_id") != "port1/systems/zero/oauth-clients/op" ||
				form.Get("client_assertion_type") != "urn:ietf:params:oauth:client-assertion-type:jwt-bearer" {
				t.Fatalf("unexpected form: %v", form)
			}
			claims := jwt.MapClaims{}
			parsed, err := jwt.ParseWithClaims(form.Get("client_assertion"), claims, func(*jwt.Token) (any, error) { return tc.pub, nil },
				jwt.WithValidMethods([]string{tc.wantAlg}))
			if err != nil || !parsed.Valid {
				t.Fatalf("assertion does not verify: %v", err)
			}
			if tc.keyID != "" && parsed.Header["kid"] != tc.keyID {
				t.Fatalf("kid header = %v", parsed.Header["kid"])
			}
			if tc.keyID == "" {
				if _, has := parsed.Header["kid"]; has {
					t.Fatal("kid header must be absent when --kid is not given")
				}
			}
			if claims["iss"] != "port1/systems/zero/oauth-clients/op" || claims["sub"] != claims["iss"] || claims["aud"] != srv.URL+"/auth/token" {
				t.Fatalf("unexpected claims: %v", claims)
			}
			iat, _ := claims["iat"].(float64)
			exp, _ := claims["exp"].(float64)
			if exp-iat > 240 || exp <= iat || time.Since(time.Unix(int64(iat), 0)) > time.Minute {
				t.Fatalf("bad iat/exp: %v %v", iat, exp)
			}
			if jti, _ := claims["jti"].(string); len(jti) < 22 { // 128 bits in base64url
				t.Fatalf("jti too short: %q", jti)
			}
			if storage.saved == nil || storage.saved.AccessToken != "operational-token" || storage.saved.RefreshToken != "" {
				t.Fatalf("stored credentials = %+v; want a non-refreshable token", storage.saved)
			}
		})
	}
}

func TestClientCredentialsLoginsUseDistinctJTIs(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	path := writeKey(t, key)
	srv := newTokenServer(t)
	svc := NewOperatorLogin(srv.Client(), &mockTokenStorage{}, nil)
	for i := 0; i < 2; i++ {
		if err := svc.ClientCredentials(context.Background(), ClientCredentialsParams{AuthURL: srv.URL, ClientID: "c", KeyFile: path}); err != nil {
			t.Fatal(err)
		}
	}
	var jtis []string
	for _, f := range srv.forms {
		claims := jwt.MapClaims{}
		_, _, err := jwt.NewParser().ParseUnverified(f.Get("client_assertion"), claims)
		if err != nil {
			t.Fatal(err)
		}
		jtis = append(jtis, claims["jti"].(string))
	}
	if jtis[0] == jtis[1] {
		t.Fatalf("jti reused: %v", jtis)
	}
}

type fakeRunner struct {
	name string
	args []string
	out  string
	err  error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.name, f.args = name, args
	return []byte(f.out + "\n"), f.err
}

func gcpLogin(srv *tokenServer, storage TokenStorage, runner CommandRunner) OperatorLogin {
	login := NewOperatorLogin(srv.Client(), storage, runner).(*operatorLogin)
	login.iamURL = srv.URL
	return login
}

func TestGCPImpersonatePassesAudienceAndKeepsIDTokenOffDisk(t *testing.T) {
	srv := newTokenServer(t)
	storage := &mockTokenStorage{}
	runner := &fakeRunner{out: "user-access-token"}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	err := gcpLogin(srv, storage, runner).GCPImpersonate(context.Background(), GCPParams{AuthURL: srv.URL, ServiceAccount: "op@proj.iam.gserviceaccount.com", Audience: "aud-123"})
	if err != nil {
		t.Fatal(err)
	}
	if runner.name != "gcloud" || strings.Join(runner.args, " ") != "auth print-access-token" {
		t.Fatalf("gcloud invocation = %s %v", runner.name, runner.args)
	}
	if len(srv.mints) != 1 {
		t.Fatalf("generateIdToken calls = %d", len(srv.mints))
	}
	mint := srv.mints[0]
	if mint.path != "/v1/projects/-/serviceAccounts/op@proj.iam.gserviceaccount.com:generateIdToken" ||
		mint.authorization != "Bearer user-access-token" || mint.body["audience"] != "aud-123" || mint.body["includeEmail"] != true {
		t.Fatalf("unexpected generateIdToken call: %+v", mint)
	}
	form := srv.forms[0]
	if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:token-exchange" || form.Get("subject_token") != "google-id-token-value" ||
		form.Get("subject_token_type") != "urn:ietf:params:oauth:token-type:id_token" || form.Get("audience") != "urn:portos:operations" {
		t.Fatalf("unexpected exchange form: %v", form)
	}
	if storage.saved == nil || storage.saved.AccessToken != "operational-token" || storage.saved.RefreshToken != "" {
		t.Fatalf("stored credentials = %+v", storage.saved)
	}
	if strings.Contains(storage.saved.AccessToken, "google-id-token-value") {
		t.Fatal("the stored credential must be the Port OS token, not the ID token")
	}
	// Nothing in the (redirected) home or temp directory may contain the ID token.
	_ = filepath.WalkDir(tmp, func(path string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			if data, _ := os.ReadFile(path); strings.Contains(string(data), "google-id-token-value") {
				t.Errorf("ID token written to disk at %s", path)
			}
		}
		return nil
	})
}

func TestGCPImpersonateRequiresAudience(t *testing.T) {
	runner := &fakeRunner{out: "x"}
	svc := NewOperatorLogin(http.DefaultClient, &mockTokenStorage{}, runner)
	svc.(*operatorLogin).iamURL = "http://127.0.0.1:1"
	if err := svc.GCPImpersonate(context.Background(), GCPParams{AuthURL: "http://127.0.0.1:1", ServiceAccount: "sa"}); err == nil {
		t.Fatal("expected an error without an audience")
	}
	if runner.name != "" {
		t.Fatal("gcloud must not run without an audience")
	}
}

func TestGCPImpersonateFailurePaths(t *testing.T) {
	cases := map[string]struct {
		runner  *fakeRunner
		sa      string
		mintErr int
	}{
		"gcloud fails":        {runner: &fakeRunner{err: errors.New("exit status 1")}, sa: "sa@proj.iam.gserviceaccount.com"},
		"empty access token":  {runner: &fakeRunner{out: ""}, sa: "sa@proj.iam.gserviceaccount.com"},
		"mint denied":         {runner: &fakeRunner{out: "tok"}, sa: "sa@proj.iam.gserviceaccount.com", mintErr: http.StatusForbidden},
		"unsafe service acct": {runner: &fakeRunner{out: "tok"}, sa: "sa&calc"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newTokenServer(t)
			srv.mintErr = tc.mintErr
			storage := &mockTokenStorage{}
			err := gcpLogin(srv, storage, tc.runner).GCPImpersonate(context.Background(),
				GCPParams{AuthURL: srv.URL, ServiceAccount: tc.sa, Audience: "aud"})
			if err == nil || len(srv.forms) != 0 || storage.saved != nil {
				t.Fatalf("expected failure with no exchange; err=%v", err)
			}
		})
	}
}

func TestOperatorLoginReplacesExistingSession(t *testing.T) {
	srv := newTokenServer(t)
	storage := &mockTokenStorage{creds: &Credentials{AccessToken: "old", RefreshToken: "r", DPoPPrivateKey: "k"}}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	err := NewOperatorLogin(srv.Client(), storage, nil).ClientCredentials(context.Background(),
		ClientCredentialsParams{AuthURL: srv.URL, ClientID: "c", KeyFile: writeKey(t, key)})
	if err != nil {
		t.Fatal(err)
	}
	if storage.saved.RefreshToken != "" || storage.saved.DPoPPrivateKey != "" || storage.saved.AccessToken != "operational-token" {
		t.Fatalf("operator login replaces the whole session: %+v", storage.saved)
	}
}

func TestClientCredentialsRejectsSmallRSAKey(t *testing.T) {
	srv := newTokenServer(t)
	key, _ := rsa.GenerateKey(rand.Reader, 1024)
	err := NewOperatorLogin(srv.Client(), &mockTokenStorage{}, nil).ClientCredentials(context.Background(),
		ClientCredentialsParams{AuthURL: srv.URL, ClientID: "c", KeyFile: writeKey(t, key)})
	if err == nil || len(srv.forms) != 0 {
		t.Fatalf("expected a key-size error, got %v", err)
	}
}

func TestFreshOperationalTokenIsUsableWithoutRefresh(t *testing.T) {
	token := generateTestJWTWithExpiry(t, time.Now().Add(290*time.Second))
	svc := newTestService(&mockTokenStorage{creds: &Credentials{AccessToken: token}})
	got, err := svc.GetAccessToken(context.Background())
	if err != nil || got != token {
		t.Fatalf("a fresh 300 s token without refresh must be usable: %v", err)
	}
	ok, _ := svc.IsAuthenticated(context.Background())
	if !ok {
		t.Fatal("IsAuthenticated must be true for a fresh operational token")
	}
	expiring := generateTestJWTWithExpiry(t, time.Now().Add(3*time.Second))
	svc = newTestService(&mockTokenStorage{creds: &Credentials{AccessToken: expiring}})
	if _, err := svc.GetAccessToken(context.Background()); err == nil {
		t.Fatal("a token inside the skew must be rejected")
	}
}
