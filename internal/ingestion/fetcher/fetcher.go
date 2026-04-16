package fetcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-shiori/go-readability"
)


const (
	ContentTypeHTML = "text/html"
)

var (
	ErrInvalidURL             = errors.New("invalid web URL")
	ErrFetchFailed            = errors.New("failed to fetch content")
	ErrUnexpectedContentType  = errors.New("unexpected content type")
	ErrUnexpectedStatusCode   = errors.New("unexpected status code")
	ErrEmptyContent           = errors.New("fetched content is empty")
	ErrBodyTooLarge           = errors.New("response body too large")
	ErrUnsupportedURLScheme   = errors.New("unsupported URL scheme")
)

// http.Transport configuration that http.Client will use to make requests.
type HTTPTransportConfig struct {
	MaxBodyBytes int64
	MaxAttempts int
	SingleAttemptTimeout time.Duration
	TotalTimeout time.Duration
	RetryDelay time.Duration
	MaxRetryDelay time.Duration
	MaxRedirects int
	UserAgent string
}

func DefaultHTTPTransportConfig() *HTTPTransportConfig {
	return &HTTPTransportConfig{
		MaxBodyBytes: 10 * 1024 * 1024, // 10 MB
		MaxAttempts: 3,
		SingleAttemptTimeout: 15 * time.Second,
		TotalTimeout: 90 * time.Second,
		RetryDelay: 500 * time.Millisecond,
		MaxRetryDelay: 5 * time.Second,
		MaxRedirects: 8,
		UserAgent: "summerizer-bot/1.0 (+https://example.com)",
	}
}


type FetcherErrors struct{
	URL string
	StatusCode int
	RetryAfter time.Duration
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
	http_cfg *HTTPTransportConfig
}

func NewFetcher() *Fetcher {

	cfg := DefaultHTTPTransportConfig()

	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		ForceAttemptHTTP2: true,
		MaxIdleConns: 100,
		MaxIdleConnsPerHost: 10,
		MaxConnsPerHost: 10,
		IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: 8 * time.Second,
		ResponseHeaderTimeout: 12 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DialContext: (&net.Dialer{
			Timeout: 6 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	client := &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= cfg.MaxRedirects {
				return  fmt.Errorf("too many redirects: %w", ErrFetchFailed)
			}
			return nil
		},
		Timeout: 0,
	}

	return  &Fetcher{
		httpClient: client,
		http_cfg: cfg,
	}
}


func (f *Fetcher) Fetch(parentCtx context.Context, rawURL string) (*RawContent, error) {

	parsedURL, err := url.Parse(rawURL)
	if err != nil ||  parsedURL.Host == "" {
		return nil, &FetcherErrors{URL: rawURL, Err: ErrInvalidURL , StatusCode: http.StatusBadRequest}
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return  nil , &FetcherErrors{URL: rawURL , StatusCode: http.StatusBadRequest , Err: ErrUnsupportedURLScheme}
	}

	// parent context (90s timeout) will be managed by the caller (Pool) to ensure that the total time spent on fetching a source does not exceed the defined limit, including retries. Each individual fetch attempt will have its own timeout defined in the HTTPTransportConfig, allowing for better control over each request while still respecting the overall time constraint for processing a source.
	// define a child context for the total timeout of the fetch operation, which includes all retry attempts. This ensures that the entire fetch process does not exceed the specified total timeout, even if multiple retries are needed.
	totalFetchCtx, cancel := context.WithTimeout(parentCtx, f.http_cfg.TotalTimeout)
	defer cancel()

	var lastFetchErr *FetcherErrors

	for attempt := 1; attempt <= f.http_cfg.MaxAttempts; attempt++ {
		// define a child context for the single attempt timeout, which ensures that each individual fetch attempt does not exceed the specified duration. .
		attemptCtx , attemptCancel := context.WithTimeout(totalFetchCtx , f.http_cfg.SingleAttemptTimeout)
		content , fErr := f.fetchOnce(attemptCtx, parsedURL)

		// important to call attemptCancel() before any continue or return statement to avoid context leaks and ensure that resources are properly released after each fetch attempt, regardless of the outcome.
		attemptCancel() // don't defer it

		if fErr == nil {
			return content, nil
		}

		lastFetchErr = fErr

		shouldRetry , wait := f.shouldRetry(fErr)

		if !shouldRetry || attempt == f.http_cfg.MaxAttempts {
			return nil, fErr
		}

		if wait == 0 {
			wait = f.retryDelay(attempt)
		}

		if err := sleepWithContext(totalFetchCtx, wait); err != nil {
			return  nil , &FetcherErrors{
				URL: rawURL,
				Err: errors.Join(ErrFetchFailed , err),
				StatusCode: http.StatusRequestTimeout,
			}
		}

	}

	return  nil , lastFetchErr
}

func (f *Fetcher) fetchOnce(ctx context.Context , u *url.URL) (*RawContent, *FetcherErrors) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, &FetcherErrors{URL: u.String(), Err: errors.Join(ErrFetchFailed , err) , StatusCode: http.StatusBadRequest}
	}

	req.Header.Set("User-Agent", f.http_cfg.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
  req.Header.Set("Accept-Language", "en-US,en;q=0.9")
  req.Header.Set("Cache-Control", "no-cache")

	resp, err := f.httpClient.Do(req)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err , context.DeadlineExceeded) || errors.Is(err , context.Canceled) {
			status = http.StatusRequestTimeout
		}
		return nil, &FetcherErrors{URL: u.String(), Err: errors.Join(ErrFetchFailed , err) , StatusCode: status}
	}

	// no matter what the outcome is, we need to ensure that the response body is closed to prevent resource leaks.
	// This also helps the TCP connections to be reused efficiently, which is important for performance when making multiple requests to the same host under specified timeouts
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK{
		return  nil , &FetcherErrors{
			URL: u.String(),
			StatusCode: resp.StatusCode,
			Err: ErrUnexpectedStatusCode,
			RetryAfter: rateLimitRetryAfter(resp.Header.Get("Retry-After")),
		}
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "" && !strings.Contains(contentType , ContentTypeHTML) && !strings.Contains(contentType , "application/xhtml+xml") {
		return nil, &FetcherErrors{URL: u.String(), StatusCode: http.StatusUnsupportedMediaType, Err: ErrUnexpectedContentType}
	}

	// read 1 extra byte to check if the body exceeds the maximum allowed size.
	body , err := io.ReadAll(io.LimitReader(resp.Body , f.http_cfg.MaxBodyBytes+1))

	if err != nil {
		return  nil , &FetcherErrors{
			URL: u.String(),
			StatusCode: http.StatusBadGateway,
			Err : errors.Join(ErrFetchFailed , err),
		}
	}

	if int64(len(body)) > f.http_cfg.MaxBodyBytes {
		return nil, &FetcherErrors{URL: u.String(), StatusCode: http.StatusRequestEntityTooLarge, Err: ErrBodyTooLarge}
	}

	if len(body) == 0 {
		return nil, &FetcherErrors{URL: u.String(), StatusCode: http.StatusNoContent, Err: ErrEmptyContent}
	}

	article , err := readability.FromReader(bytes.NewReader(body) , u)

	if err != nil {
		return nil, &FetcherErrors{URL: u.String(), StatusCode: http.StatusBadGateway, Err: errors.Join(ErrFetchFailed , err)}
	}

	if strings.TrimSpace(article.Content) == "" {
		return  nil , &FetcherErrors{
			URL : u.String(),
			StatusCode: http.StatusUnprocessableEntity,
			Err : ErrEmptyContent,
		}
	}

	// url could have been changed during redirects, so we update it to the final URL after fetching the content.
	finalURL := u.String()

	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	return &RawContent{
		URL:    finalURL,
		TextContent: article.TextContent,
		HTMLContent: article.Content,
		Title:  article.Title,
	}, nil
}

func (f * Fetcher) shouldRetry(fErr *FetcherErrors) (bool , time.Duration) {
	if fErr == nil {
		return false , 0
	}

	if isRetryAbleStatus(fErr.StatusCode) {
		return true , fErr.RetryAfter
	}

	// is it a network error that is retryable?
	var netErr net.Error
	if errors.As(fErr.Err, &netErr) {
		return true , 0
	}

	// context here is the attempt context, if it was canceled or deadline exceeded .
	if errors.Is(fErr.Err, context.DeadlineExceeded) || errors.Is(fErr.Err, context.Canceled) {
		return false , 0
	}

	return false , 0
}
func isRetryAbleStatus(statusCode int) bool {
	switch statusCode {
		case 408, 425, 429, 500, 502, 503, 504:
			return true
		default:
			return false
	}
}
// retryDelay returns an exponential backoff with jitter.
//
// Imagine you have 50 workers all trying to fetch from the same news site. The site goes down briefly at 14:00:00. All 50 workers fail at exactly 14:00:00. With a fixed 500ms sleep, all 50 retry at exactly 14:00:00.500. The site just came back up — and immediately gets slammed with 50 simultaneous requests again. It goes down again. This cycle repeats. This is the thundering herd problem.
//
// random delay:
//  - prevents all workers from retrying at the same instant
//  - gives a struggling upstream time to recover
//
// How:
//  - grow the base delay exponentially per attempt
// 	- cap the base delay at MaxRetryDelay
//  - add random jitter on top of the base delay
//
// Example with RetryDelay=500ms:
//  - attempt 1: 500ms + jitter(0..500ms)
//  - attempt 2: 1s + jitter(0..500ms)
//  - attempt 3: 2s + jitter(0..500ms)
//
// Now those 50 workers that all failed at the same moment each pick a different random wait. Instead of all 50 hitting the server at 14:00:00.500, they spread out across a window — maybe 5 at 14:00:00.520, 8 at 14:00:00.650, and so on. The server gets a manageable trickle instead of a spike.
//
// Note:
// - jitter is added after the cap, so the final wait can be slightly above
//   MaxRetryDelay.
func (f *Fetcher) retryDelay(attempt int) time.Duration {
	delay := time.Duration(1<<(attempt -1)) * f.http_cfg.RetryDelay

	if delay > f.http_cfg.MaxRetryDelay {
		delay = f.http_cfg.MaxRetryDelay
	}

	jitter := time.Duration(rand.Int63n(int64(f.http_cfg.RetryDelay)))

	return delay + jitter
}

// sleepWithContext allows sleeping for a specified duration while respecting context cancellation .
//   - If the totalfetchCtx is canceled or limit exceeded while sleeping for another attempt, it returns an error, allowing the caller to handle the cancellation appropriately.
//  - This is important to prevent unnecessary waiting and to ensure that the fetch operation can be aborted in a timely manner if the overall time limit for processing a source is reached.
// So , the flow be like this :
//    - in the main parent context at source level (90s context window) , if that's exceeded the totalFetchCtx which is under that parent context will be canceled and as the attempt context children are under that totalFetchCtx , they will also be canceled
func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
	}
}


// rateLimitRetryAfter parses the "Retry-After" header value and returns the duration to wait before retrying.
//
// It handles both formats of the "Retry-After" header:
// 1. A delay in seconds (e.g., "120").
// 2. An HTTP date (e.g., "Wed, 21 Oct 2020 07:28:00 GMT").
//
// If the header is empty or cannot be parsed, it returns a zero duration, indicating that the caller can decide on a default retry delay.
func rateLimitRetryAfter(retryAfterHeader string) time.Duration {
	if retryAfterHeader == "" {
		return 0
	}

	if sec , err := strconv.Atoi(strings.TrimSpace(retryAfterHeader)); err == nil && sec > 0 {
		return time.Duration(sec) * time.Second
	}

	// if the header is not a valid integer, try to parse it as an HTTP date.
	// e.g. Retry-After: Wed, 21 Oct 2020 07:28:00 GMT
	if t, err := http.ParseTime(retryAfterHeader); err == nil {
		if d := time.Until(t) ; d > 0 {
			return d
		}
	}

	return 0
}