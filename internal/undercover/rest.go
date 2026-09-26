package undercover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// RESTClient is the Client the CI job uses: GitHub's REST API with the
// workflow's own token. HTTP carries the timeout.
type RESTClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Get answers the body of a successful GET.
func (r RESTClient) Get(ctx context.Context, path string) ([]byte, error) {
	return r.do(ctx, http.MethodGet, path, nil)
}

// Patch sends fields as a JSON object.
func (r RESTClient) Patch(ctx context.Context, path string, fields map[string]string) error {
	body, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = r.do(ctx, http.MethodPatch, path, body)
	return err
}

func (r RESTClient) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(r.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPError{Method: method, Path: path, Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	return data, nil
}

// HTTPError is a non-2xx answer, kept typed so a caller can tell a refused
// write (403: a read-only token) from every other failure.
type HTTPError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: %d %s: %s", e.Method, e.Path, e.Status, http.StatusText(e.Status), e.Body)
}
