package main

import (
	"github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
	"net/http"
)

var (
	router         = mux.NewRouter()
	healtcheckPort = ":10241"
)

func startHealthcheck() {
	router.HandleFunc("/healthz", healtcheck).Methods("GET", "HEAD").Name("Healthcheck")
	logrus.Info("Healthcheck handler is listening on ", healtcheckPort)
	logrus.Fatal(http.ListenAndServe(healtcheckPort, router))
}

func healtcheck(w http.ResponseWriter, req *http.Request) {
	// 1) test controller
	if !lbc.IsHealthy() {
		logrus.Error("Healtcheck failed for LB controller")
		http.Error(w, "Healtcheck failed for LB controller", http.StatusInternalServerError)
	} else if !lbp.IsHealthy() {
		logrus.Error("Healtcheck failed for LB provider")
		http.Error(w, "Healtcheck failed for LB provider", http.StatusInternalServerError)
	} else {
		w.Write([]byte("OK"))
	}
}
