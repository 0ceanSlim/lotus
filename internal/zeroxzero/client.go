package zeroxzero

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
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

func (c *Client) Upload(data []byte, filename string) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(data)); err != nil {
		return "", fmt.Errorf("write form data: %w", err)
	}
	w.Close()

	req, err := http.NewRequest(http.MethodPost, c.baseUrl+"/", &body)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("0x0 upload status %d", resp.StatusCode)
	}

	urlBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	return strings.TrimSpace(string(urlBytes)), nil
}

func (c *Client) Delete(fileUrl string) error {
	req, err := http.NewRequest(http.MethodDelete, fileUrl, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
