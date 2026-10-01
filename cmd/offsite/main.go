package main

import (
	"log"
	"net/http"
	"os"
	"time"

	offsitecalculator "github.com/lee101/offsitecalculator"
	"github.com/lee101/offsitecalculator/server"
)

func main() {
	srv, err := server.New(server.ConfigFromEnv(offsitecalculator.Web()))
	if err != nil {
		log.Fatal(err)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	httpSrv := &http.Server{Addr: ":" + port, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("offsitecalculator listening on :%s", port)
	log.Fatal(httpSrv.ListenAndServe())
}
