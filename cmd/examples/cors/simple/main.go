package main

import (
	"flag"
	"log"
	"net/http"
)

const html = `
<!DOCTYPE html>
<html lang="en">
	<head>
		<meta charset="UTF-8">
		<meta name="viewport" content="width=device-width, initial-scale=1.0">
		<title>CORS Example</title>
	</head>
	<body>
		<h1>CORS Example</h1>
		<div id="result"></div>
		<script>
			document.addEventListener('DOMContentLoaded', function() {
				fetch("http://localhost:4000/v1/healthcheck")
				.then(function (response) {
					return response.text();
				})
				.then(function (text) {
					document.getElementById("result").innerHTML = text;
				})
				.catch(function(err) {
					document.getElementById("result").innerHTML = err;
				});
			});
		</script>
	</body>
</html>
`

func main() {
	addr := flag.String("addr", ":9000", "HTTP network address")
	flag.Parse()

	log.Printf("starting server on %s", *addr)

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
	}

	err := http.ListenAndServe(*addr, http.HandlerFunc(handler))
	if err != nil {
		log.Fatal(err)
	}
}
