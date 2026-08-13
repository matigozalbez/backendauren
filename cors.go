package main

import (
	"fmt"
	"net/http"
)

func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origen := r.Header.Get("Origin")
	fmt.Println("ORIGEN RECIBIDO:", "["+origen+"]")

	// Si el navegador manda un origen, se lo devolvemos tal cual para abrir las puertas a todo.
	// Si no manda origen (como Postman o curl), mandamos comodín.
	if origen != "" {
		w.Header().Set("Access-Control-Allow-Origin", origen)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	} else {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}

	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Secret")

	// Si es una petición OPTIONS (preflight), cortamos acá con 200 OK
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
}
