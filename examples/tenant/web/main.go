// Command web greets with the message from $MESSAGE. It never prints $TOKEN, only whether the
// platform provided it.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		host, _ := os.Hostname()
		fmt.Fprintf(w, "%s\nhost: %s\ntoken set: %t\n", os.Getenv("MESSAGE"), host, os.Getenv("TOKEN") != "")
	})
	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
