package main

import (
	"bytes"
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
	ErrInvalidURL = fmt.Errorf("invalid web URL")
	ErrFetchFailed = fmt.Errorf("failed to fetch content")
	ErrUnexpectedContentType = fmt.Errorf("unexpected content type")
	ErrUnexpectedStatusCode = fmt.Errorf("unexpected status code")
	ErrEmptyContent = fmt.Errorf("fetched content is empty")
)

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
		return nil, ErrInvalidURL
	}

	resp, err := f.httpClient.Get(rawURL)

	if err != nil {
		return nil , fmt.Errorf("%w: %s", ErrFetchFailed, err.Error())
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%w: got %d", ErrUnexpectedStatusCode, resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType , ContentTypeHTML) {
		return nil, fmt.Errorf("%w: got %s", ErrUnexpectedContentType, contentType)
	}

	body := io.LimitReader(resp.Body, maxBodySize)
	b , err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %s", ErrFetchFailed, err.Error())
	}


	article , err := readability.FromReader(bytes.NewReader(b), parsedURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrFetchFailed, err.Error())
	}

	if strings.TrimSpace(article.TextContent) == "" {
			return nil, ErrEmptyContent
	}

	return &RawContent{
		Title: article.Title,
		TextContent: article.TextContent,
		HTMLContent: article.Content,
		URL: rawURL,
	}, nil

}

func main(){
	f := NewFetcher()

	urls := []string{
		"https://gobyexample.com/http-clients",
		"https://pkg.go.dev/io#LimitReader",
	}

	for _,  u := range urls {

		content, err := f.Fetch(u)
		if err != nil {
			fmt.Println("Error:", err)
			continue
		}

		fmt.Printf("Title : %s\n" , content.Title)
		fmt.Printf("Text Content : %s\n" , content.TextContent)
		fmt.Printf("HTML Content : %s\n" , content.HTMLContent)
		fmt.Printf("URL : %s\n" , content.URL)
		fmt.Println("--------------------------------------------------")


	}
}