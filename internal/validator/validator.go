package validator

import (
	"net/url"
	"regexp"
	"slices"
	"strings"


)

var (
	EmailRX   = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")
	YouTubeRX = regexp.MustCompile(`^(https?://)?(www\.)?(youtube\.com/(watch\?v=|embed/)|youtu\.be/)[\w-]+`)
)

/*
- Methods on *Validator should only be things that mutate or inspect the error map

- The rule is simple:
		- if it touches v.Errors, it's a method.
		- If it's just a boolean check on a value, it's a standalone function that gets passed into v.Check().

*/


const (
	SourceTypeYouTube = "youtube"
	SourceTypePDF     = "pdf"
	SourceTypeWeb     = "web"
	InvalidSourceType   = ""
)

type Validator struct {
	Errors map[string]string
}

func New() *Validator {
	return &Validator{
		Errors: make(map[string]string),
	}
}

func (v *Validator) Valid() bool {
	return len(v.Errors) == 0
}

func (v *Validator) AddError(key, message string) {
	if _, exists := v.Errors[key]; !exists {
		v.Errors[key] = message
	}
}

func (v *Validator) Check(ok bool, key, message string) {
	if !ok {
		v.Errors[key] = message
	}
}

// NotBlank returns true if the string is not empty after trimming whitespace.
func NotBlank(value string) bool {
	return strings.TrimSpace(value) != ""
}

// MinChars returns true if the string has at least n characters.
func MinChars(value string, n int) bool {
	return len([]rune(value)) >= n
}

// MaxChars returns true if the string has at most n characters.
func MaxChars(value string, n int) bool {
	return len([]rune(value)) <= n
}

// ValidURL returns true if the string is a well-formed absolute HTTP(S) URL.
func ValidURL(value string) bool {
	u, err := url.ParseRequestURI(value)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func PermittedValue[T comparable](value T, permittedValues ...T) bool {
	return slices.Contains(permittedValues, value)
}

func (v *Validator) Matches(val string, reg regexp.Regexp) bool {
	return reg.MatchString(val)
}

func Unique[T comparable](vals []T) bool {
	mp := make(map[T]bool)

	for _, val := range vals {
		mp[val] = true
	}

	return len(mp) == len(vals)
}

func DetectSourceType(rawURL string) string {

	if strings.TrimSpace(rawURL) == "" {
		return InvalidSourceType
	}

	if YouTubeRX.MatchString(rawURL) {
		return SourceTypeYouTube
	}

	u, err := url.Parse(rawURL)
	if err == nil && strings.HasSuffix(strings.ToLower(u.Path), ".pdf") {
		return SourceTypePDF
	}

	return SourceTypeWeb
}
