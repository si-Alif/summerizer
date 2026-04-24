package main

import (
	"flag"
	"log"
	"net/http"
)

const html = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Document</title>
</head>
<body>
    <h1>Pre-flight CORS Request</h1>
    <div id="output"></div>
    <script>
      document.addEventListener("DOMContentLoaded", function() {
        fetch("http://localhost:4000/v1/tokens/authentication", {
          method: "POST",
          headers: {
            "Content-Type": "application/json"
          },
          body: JSON.stringify(
            {
              "email": "john@example.com",
              "password": "securepassword123"
            }
          )
        }).then(
          function (response) {
            response.text().then(function (text) {
              document.getElementById("output").innerHTML = text;
            });
          },
          function(err) {
            console.error("Error:", err);
          }
        );
      });
    </script>
</body>
</html>`

func main() {
	addr := flag.String("addr" , ":9000" , "Server address")
	flag.Parse()

	err := http.ListenAndServe(*addr , http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(html))
	}))

	log.Fatal(err)
}