package main

import (
	"errors"
	"expvar"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/validator"
	"github.com/tomasen/realip"
	"golang.org/x/time/rate"
)

func(app *application) recoverPanic(next http.Handler) http.Handler{
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func(){
			if err := recover(); err != nil{
				w.Header().Set("Connection", "close")
				app.serverErrorResponse(w, r, fmt.Errorf("%v", err))
				return
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (app *application) rateLimit(next http.Handler) http.Handler{
	if !app.config.limiter.enabled{
		return next
	}

	type client struct{
		limiter *rate.Limiter
		lastSeen time.Time
	}

	var (
		mu sync.Mutex
		clients = make(map[string]*client)
	)

	go func(){
		for {
			time.Sleep(time.Minute)

			mu.Lock()
			for ip, client := range clients{
				if time.Since(client.lastSeen) > 3*time.Minute{
					delete(clients, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := realip.FromRequest(r)

		mu.Lock()

		if _, found := clients[ip]; !found{
			clients[ip] = &client{
				limiter: rate.NewLimiter(rate.Limit(app.config.limiter.rps), app.config.limiter.burst),
			}
		}
		clients[ip].lastSeen = time.Now()

		if !clients[ip].limiter.Allow(){
			mu.Unlock()
			app.rateLimitExceededResponse(w, r)
			return
		}

		mu.Unlock()

		next.ServeHTTP(w, r)
	})
}

func (app *application) authenticate(next http.Handler) http.Handler{
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Authorization")
		authorizationHeader := r.Header.Get("Authorization")

		if authorizationHeader == ""{
			r = app.SetUserInRequestContext(r , data.AnonymousUser)
			next.ServeHTTP(w, r)
			return
		}

		headerParts := strings.Split(authorizationHeader, " ")
		if len(headerParts) != 2 || headerParts[0] != "Bearer"{
			app.invalidAuthenticationTokenResponse(w, r)
			return
		}

		token := headerParts[1]

		v := validator.New()

		data.ValidateTokenPlaintext(v, token)

		if !v.Valid(){
			app.invalidAuthenticationTokenResponse(w, r)
			return
		}

		user, err := app.models.Users.GetForToken(data.ScopeAuthentication, token)
		if err != nil{
			switch {
			case errors.Is(err , data.ErrRecordNotFound):
				app.invalidAuthenticationTokenResponse(w, r)
			default:
				app.serverErrorResponse(w, r, err)
			}
			return
		}

		r = app.SetUserInRequestContext(r, user)
		next.ServeHTTP(w, r)
	})
}

func (app *application) requireAuthenticatedUser(next http.HandlerFunc) http.HandlerFunc{
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := app.GetUserFromSubsequentRequestContext(r)
		if user.IsAnonymous(){
			app.AuthenticationRequiredResponse(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (app *application) requireActivatedUser(next http.HandlerFunc) http.HandlerFunc{
	fn:= http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := app.GetUserFromSubsequentRequestContext(r)
		if !user.Activated{
			app.ActivationRequiredResponse(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})

	return  app.requireAuthenticatedUser(fn)
}

func (app *application) enableCORS(next http.Handler) http.Handler{
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Origin cz based on trusted origins or not , the response will vary
		w.Header().Add("Vary" , "Origin")
		// Access-Control-Request-Method is for preflight request detection mainly / requests in general, based on the presence of this header the response will vary
		w.Header().Add("Vary" , "Access-Control-Request-Method")

		if origin != ""{
			for i := range app.config.cors.trustedOrigins{
				if origin == app.config.cors.trustedOrigins[i]{
					w.Header().Set("Access-Control-Allow-Origin", origin)

					if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
						w.Header().Set("Access-Control-Allow-Methods", "OPTIONS, PUT, PATCH, DELETE")
						w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
						w.WriteHeader(http.StatusOK)
						return
					}
					break
				}
			}
		}
		next.ServeHTTP(w , r)
	})
}


type metricsResponseWriter struct{
	wrapped http.ResponseWriter
	statusCode int
	headerWritten bool
}

func newMetricsResponseWriter(w http.ResponseWriter) *metricsResponseWriter{
	return  &metricsResponseWriter{
		wrapped: w,
		statusCode: http.StatusOK,
	}
}

func (mw *metricsResponseWriter) Header() http.Header{
	return mw.wrapped.Header()
}

func (mw *metricsResponseWriter) WriteHeader(statusCode int){
	mw.wrapped.WriteHeader(statusCode)

	if !mw.headerWritten{
		mw.statusCode = statusCode
		mw.headerWritten = true
	}
}

func (mw *metricsResponseWriter) Write(b []byte) (int, error){
	mw.headerWritten = true

	return mw.wrapped.Write(b)
}

func (mw *metricsResponseWriter) Unwrap() http.ResponseWriter{
	return mw.wrapped
}

func (app *application) metrics(next http.Handler) http.Handler{
	var(
		totalRequestsReceived = expvar.NewInt("total_requests_received")
		totalResponsesSent = expvar.NewInt("total_responses_sent")
		totalProcessingTimeMicroseconds = expvar.NewInt("total_processing_time_μs")
		totalResponsesByStatusCode = expvar.NewMap("total_responses_by_status_code")
	)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := time.Now()

		totalRequestsReceived.Add(1)

		mw := newMetricsResponseWriter(w)

		// call the next handler in the chain with our custom metricsResponseWriter(mw) which will capture the status code and count the number of bytes written in the response
		next.ServeHTTP(mw, r)

		totalResponsesSent.Add(1)

		totalResponsesByStatusCode.Add(strconv.Itoa(mw.statusCode), 1)

		elapsed := time.Since(st).Microseconds()
		totalProcessingTimeMicroseconds.Add(elapsed)
	})
}