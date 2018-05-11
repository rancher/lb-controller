package main

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/leodotcloud/log"
)

var (
	router         = mux.NewRouter()
	healtcheckPort = ":10241"
)

func startHealthcheck() {
	router.HandleFunc("/healthz", healtcheck).Methods("GET", "HEAD").Name("Healthcheck")
	log.Info("Healthcheck handler is listening on ", healtcheckPort)
	log.Fatal(http.ListenAndServe(healtcheckPort, router))
}

func healtcheck(w http.ResponseWriter, req *http.Request) {
	// 1) test controller
	if !lbc.IsHealthy() {
		log.Error("Healtcheck failed for LB controller")
		http.Error(w, "Healtcheck failed for LB controller", http.StatusInternalServerError)
	} else if !lbp.IsHealthy() {
		log.Error("Healtcheck failed for LB provider")
		http.Error(w, "Healtcheck failed for LB provider", http.StatusInternalServerError)
	} else {
		w.Write([]byte("OK"))
	}
}
