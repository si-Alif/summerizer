package fetcher

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-shiori/go-readability"
)


const (
	maxBodySize = 10_000_000
	ContentTypeHTML = "text/html"
	fetchTimeout = 10
)

var (
	ErrInvalidURL = errors.New("invalid web URL")
	ErrFetchFailed = errors.New("failed to fetch content")
	ErrUnexpectedContentType = errors.New("unexpected content type")
	ErrUnexpectedStatusCode = errors.New("unexpected status code")
	ErrEmptyContent = errors.New("fetched content is empty")
)

type FetcherErrors struct{
	URL string
	StatusCode int
	Err error
}

// Error method helps the FetcherErrors type to satisfy the error interface, allowing it to be used as an "error" in Go . It provides a string representation of the error, including the URL, status code, and the underlying error message if available.
func (e *FetcherErrors) Error() string {
	if e == nil {
		return "fetcher error: <nil>"
	}

	if e.Err == nil {
		return fmt.Sprintf("fetcher error status=%d url=%s", e.StatusCode, e.URL)
	}

	return fmt.Sprintf("fetcher error status=%d url=%s: %v", e.StatusCode, e.URL, e.Err)
}

// Unwrap method allows you to retrieve the underlying error wrapped by the FetcherErrors type. This is useful for error handling and allows you to check for specific error types using errors.Is()
func (e *FetcherErrors) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Err
}

type RawContent struct {
	Title  string
	TextContent string
	HTMLContent string
	URL 	string
}


type Fetcher struct {
	httpClient *http.Client
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		httpClient: &http.Client{
			Timeout: fetchTimeout * time.Second,
		},
	}
}


func (f *Fetcher) Fetch(rawURL string) (*RawContent, error) {

	parsedURL, err := url.Parse(rawURL)
	if err != nil || (parsedURL.Scheme == "" && parsedURL.Host == "") {
		return nil, &FetcherErrors{URL: rawURL, Err: ErrInvalidURL , StatusCode: http.StatusBadRequest}
	}

	resp, err := f.httpClient.Get(rawURL)

	if err != nil {
		return nil, &FetcherErrors{URL: rawURL, Err: errors.Join(ErrFetchFailed, err), StatusCode: http.StatusServiceUnavailable}
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
			return nil, &FetcherErrors{URL: rawURL , Err : ErrUnexpectedStatusCode , StatusCode: resp.StatusCode}
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType , ContentTypeHTML) {
		return nil, &FetcherErrors{URL: rawURL, Err: ErrUnexpectedContentType, StatusCode: http.StatusUnsupportedMediaType}
	}

	body := io.LimitReader(resp.Body, maxBodySize)
	b , err := io.ReadAll(body)
	if err != nil {
		return nil, &FetcherErrors{URL: rawURL, Err: errors.Join(ErrFetchFailed, err), StatusCode: http.StatusBadGateway}
	}


	article , err := readability.FromReader(bytes.NewReader(b), parsedURL)
	if err != nil {
		return nil, &FetcherErrors{URL: rawURL, Err: errors.Join(ErrFetchFailed, err), StatusCode: http.StatusBadGateway}
	}

	if strings.TrimSpace(article.TextContent) == "" {
			return nil, &FetcherErrors{URL: rawURL, Err: ErrEmptyContent, StatusCode: http.StatusUnprocessableEntity}
	}

	return &RawContent{
		Title: article.Title,
		TextContent: article.TextContent,
		HTMLContent: article.Content,
		URL: rawURL,
	}, nil

}

