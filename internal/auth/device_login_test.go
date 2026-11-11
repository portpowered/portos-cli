package auth

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// fakeDeviceServer answers the device authorization and token endpoints and records each call.
type fakeDeviceServer struct {
	paths []string
	forms []url.Values
	dpop  []string
}

func (s *fakeDeviceServer) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	s.paths = append(s.paths, r.URL.Path)
	s.forms = append(s.forms, r.PostForm)
	s.dpop = append(s.dpop, r.Header.Get("DPoP"))
	body := `{"error":"not_found"}`
	status := http.StatusNotFound
	switch r.URL.Path {
	case "/auth/device-authorization":
		status, body = http.StatusOK, `{"device_code":"dc","user_code":"ABCD-1234","verification_uri":"https://www.example.test/oauth/verify-device","verification_uri_complete":"https://www.example.test/oauth/verify-device?user_code=ABCD-1234","expires_in":600,"interval":5}`
	case "/auth/token":
		status, body = http.StatusOK, `{"access_token":"a","refresh_token":"r","token_type":"DPoP","expires_in":3600}`
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

func TestDeviceLoginUsesTheServerEndpointsAndTheCLIClient(t *testing.T) {
	server := &fakeDeviceServer{}
	httpClient := &http.Client{Transport: server}
	svc, storage := newRefreshServiceAt(t, httpClient, refreshTestAuthBase, &Credentials{})
	svc.deviceClient = NewDeviceAuthClient(httpClient, svc.configProvider)

	var shown []string
	if err := svc.Login(context.Background(), "", func(m string) { shown = append(shown, m) }); err != nil {
		t.Fatal(err)
	}

	if want := []string{"/auth/device-authorization", "/auth/token"}; strings.Join(server.paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v, want %v", server.paths, want)
	}
	for i, form := range server.forms {
		if form.Get("client_id") != defaultCLIClientID {
			t.Fatalf("call %d client_id = %q, want the CLI client", i, form.Get("client_id"))
		}
		if server.dpop[i] == "" {
			t.Fatalf("call %d has no DPoP proof", i)
		}
	}
	if form := server.forms[1]; form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || form.Get("device_code") != "dc" {
		t.Fatalf("token form = %v", form)
	}
	if !strings.Contains(strings.Join(shown, ""), "ABCD-1234") {
		t.Fatalf("instructions %q do not carry the user code", shown)
	}
	if storage.creds.AccessToken != "a" || storage.creds.RefreshToken != "r" {
		t.Fatalf("stored credentials = %+v", storage.creds)
	}
}
