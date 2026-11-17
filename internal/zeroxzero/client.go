package zeroxzero

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Client struct {
	baseUrl    string
	httpClient *http.Client
}

func New(baseUrl string) *Client {
	return &Client{
		baseUrl:    strings.TrimRight(baseUrl, "/"),
		httpClient: &http.Client{},
	}
}

// Upload stores data on the 0x0 backend. expiresAt (unix seconds, 0 for
// the backend's own maximum) is passed as the requested expiry; the backend
// rounds it down to its own ceiling. It returns the file URL, the expiry the
// backend actually set (unix seconds, 0 if it did not say), and the
// management token, which the backend only returns for a file it did not
// already have.
func (c *Client) Upload(data []byte, filename string, expiresAt int64) (Stored, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return Stored{}, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(data)); err != nil {
		return Stored{}, fmt.Errorf("write form data: %w", err)
	}
	if expiresAt > 0 {
		if err := w.WriteField("expires", strconv.FormatInt(expiresAt*1000, 10)); err != nil {
			return Stored{}, fmt.Errorf("write expires: %w", err)
		}
	}
	w.Close()

	req, err := http.NewRequest(http.MethodPost, c.baseUrl+"/", &body)
	if err != nil {
		return Stored{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Stored{}, fmt.Errorf("upload request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Stored{}, fmt.Errorf("0x0 upload status %d", resp.StatusCode)
	}

	urlBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return Stored{}, fmt.Errorf("read response: %w", err)
	}

	out := Stored{
		Url:   strings.TrimSpace(string(urlBytes)),
		Token: resp.Header.Get("X-Token"),
	}
	if ms, err := strconv.ParseInt(resp.Header.Get("X-Expires"), 10, 64); err == nil {
		out.ExpiresAt = ms / 1000
	}
	return out, nil
}

// Stored is what the backend reports for an upload.
type Stored struct {
	Url       string
	Token     string
	ExpiresAt int64
}

// ErrNoToken means a file has no management token on record, so the backend
// will not accept a delete or expiry change for it.
var ErrNoToken = errors.New("0x0: no management token for this file")

// Delete removes a file from the backend. 0x0 manages files with a form POST
// to the file URL carrying the token from the upload.
func (c *Client) Delete(fileUrl, token string) error {
	return c.manage(fileUrl, token, url.Values{"delete": {""}}, http.StatusOK)
}

// SetExpiry changes when the backend removes a file (unix seconds).
func (c *Client) SetExpiry(fileUrl, token string, expiresAt int64) error {
	return c.manage(fileUrl, token, url.Values{"expires": {strconv.FormatInt(expiresAt*1000, 10)}}, http.StatusAccepted)
}

func (c *Client) manage(fileUrl, token string, form url.Values, want int) error {
	if token == "" {
		return ErrNoToken
	}
	form.Set("token", token)
	resp, err := c.httpClient.PostForm(fileUrl, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil // already gone
	}
	if resp.StatusCode != want {
		return fmt.Errorf("0x0 manage status %d", resp.StatusCode)
	}
	return nil
}
