package main

import (
	"aurenbackend/firebase"
	"aurenbackend/handlers"
	"log"
	"net/http"
)

const adminSecret = "hola"

func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admin-Secret") != adminSecret {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func main() {
	firebase.Init()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/admin/socios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		requireAdmin(handlers.CrearSocio(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/vincular-socio", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.VincularSocio(firebase.Client, firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/crear-usuario", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.CrearUsuario(firebase.Client, firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/verificar-vinculacion", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.VerificarVinculacion(firebase.Client, firebase.AuthClient)(w, r)
	})

	log.Println("servidor corriendo en :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
