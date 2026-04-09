package fetcher

import (
	"bytes"
	"errors"
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


func (f *Fetcher) Fetch(rawURL string) (*RawContent, *FetcherErrors) {

	parsedURL, err := url.Parse(rawURL)
	if err != nil || (parsedURL.Scheme == "" && parsedURL.Host == "") {
		return nil, &FetcherErrors{URL: rawURL, Err: ErrInvalidURL , StatusCode: http.StatusBadRequest}
	}

	resp, err := f.httpClient.Get(rawURL)

	if err != nil {
		return nil , &FetcherErrors{URL: rawURL, Err: ErrFetchFailed, StatusCode: http.StatusInternalServerError}
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
			return nil, &FetcherErrors{URL: rawURL , Err : ErrUnexpectedStatusCode , StatusCode: resp.StatusCode}
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType , ContentTypeHTML) {
		return nil, &FetcherErrors{URL: rawURL, Err: ErrUnexpectedContentType, StatusCode: http.StatusBadRequest}
	}

	body := io.LimitReader(resp.Body, maxBodySize)
	b , err := io.ReadAll(body)
	if err != nil {
		return nil, &FetcherErrors{URL: rawURL, Err: ErrFetchFailed, StatusCode: http.StatusInternalServerError}
	}


	article , err := readability.FromReader(bytes.NewReader(b), parsedURL)
	if err != nil {
		return nil, &FetcherErrors{URL: rawURL, Err: ErrFetchFailed, StatusCode: http.StatusInternalServerError}
	}

	if strings.TrimSpace(article.TextContent) == "" {
			return nil, &FetcherErrors{URL: rawURL, Err: ErrEmptyContent, StatusCode: http.StatusBadRequest}
	}

	return &RawContent{
		Title: article.Title,
		TextContent: article.TextContent,
		HTMLContent: article.Content,
		URL: rawURL,
	}, nil

}

