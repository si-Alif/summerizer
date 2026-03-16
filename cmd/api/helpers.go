package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/julienschmidt/httprouter"
	"github.com/si-Alif/summerizer/internal/validator"
)

// for better structuring of the json output where the JSON value would be labeled with a key
type envelope map[string]any

/*
- readIDParam extracts the `id` path parameter from the request context (set by httprouter),
  - converts it to a base-10 int64, and validates that it is greater than zero.
  - Use ParamsFromContext for `route/path` parameters like `/v1/movies/:id` because httprouter
    stores matched path segments in the request context.

- Use r.URL.Query() for query-string
  - values like `?page=2` or `?filter=active`,
  - since those are part of the URL's query component, not router path params.
*/
func (app *application) readIDParam(r *http.Request) (int64, error) {
	params := httprouter.ParamsFromContext(r.Context())

	id, err := strconv.ParseInt(params.ByName("id"), 10, 64)

	if err != nil || id < 1 {
		return 0, errors.New("Invalid ID parameter")
	}

	return id, nil
}


func (app *application) writeJSON(w http.ResponseWriter, status int, data envelope, headers http.Header) error {
	js, err := json.MarshalIndent(data, "", "\t")

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}

	js = append(js, '\n')

	// as go doesn't complain iterating over nil map , we don't have to do the check
	for key, val := range headers {
		w.Header()[key] = val
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(js)

	return nil

}

// readJSON reads the JSON from the request body and decodes it into the destination struct.
func (app *application) readJSON(w http.ResponseWriter, r *http.Request, dst any) error {

	r.Body = http.MaxBytesReader(w, r.Body, 1_048_576)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)

	if err != nil {

		// for comparison
		var syntaxError *json.SyntaxError
		var unmarshalTypeError *json.UnmarshalTypeError
		var invalidUnmarshalError *json.InvalidUnmarshalError
		var maxBytesError *http.MaxBytesError

		switch {
		case errors.As(err, &syntaxError):
			return fmt.Errorf("body contains badly-formed JSON (at character %d)", syntaxError.Offset)
		case errors.Is(err, io.ErrUnexpectedEOF):
			return errors.New("body contains badly-formed JSON")
		case errors.As(err, &unmarshalTypeError):
			if unmarshalTypeError.Field != "" {
				return fmt.Errorf("body contains incorrect JSON type for field %q", unmarshalTypeError.Field)
			}
			return fmt.Errorf("body contains incorrect JSON type (at character %d)", unmarshalTypeError.Offset)
		case errors.Is(err, io.EOF):
			return errors.New("body must not be empty")
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			field := strings.TrimPrefix(err.Error(), "json : unknown field ")
			return fmt.Errorf("body contains unknown key %s", field)
		case errors.As(err, &maxBytesError):
			return fmt.Errorf("body must not be larger than %d bytes", maxBytesError.Limit)
		case errors.As(err, &invalidUnmarshalError):
			panic(err)
		default:
			return err
		}

	}

	err = dec.Decode(&struct{}{})
	if !errors.Is(err, io.EOF) {
		return errors.New("body must only contain a single JSON value")
	}

	return nil

}


func (app *application) readString(vals url.Values , key,defaultValue string) string {
	val := vals.Get(key)

	if val == "" {
		return defaultValue
	}

	return val
}

func (app *application) readCSV(vals url.Values , key string, defaultValue []string) []string {
	val := vals.Get(key)

	if val == "" {
		return defaultValue
	}

	return  strings.Split(val , ",")
}

func (app *application) readInt(vals url.Values , key string, defaultValue int , v *validator.Validator) int {
	val := vals.Get(key)

	if val == "" {
		return defaultValue
	}

	i, err := strconv.Atoi(val)
	if err != nil {
		v.AddError(key, "must be an integer value")
		return defaultValue
	}
	return i
}