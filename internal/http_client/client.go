package http_client

import "net/http"

func ProvideHTTPClient() *http.Client {
	return http.DefaultClient
}
