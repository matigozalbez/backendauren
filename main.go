package main

import (
	"aurenbackend/firebase"
	"aurenbackend/handlers"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
)

var adminSecretEnv = "adminkey"

func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admin-Secret") != adminSecretEnv {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func main() {

	if err := godotenv.Load(); err != nil {
		log.Println("Aviso: No se encontro el archivo env")
	}

	handlers.RESEND_API_KEY = os.Getenv("RESEND_API_KEY")

	if val := os.Getenv("ADMIN_SECRET_KEY"); val != "" {
		adminSecretEnv = val
	} else {
		log.Println("Aviso: ADMIN_SECRET_KEY no está configurada, usando default")
	}

	firebase.Init()
	mux := http.NewServeMux()
	handlers.FirestoreClient = firebase.Client
	handlers.AuthClient = firebase.AuthClient

	err := firebase.DarAdmin("matiasgozalbez@gmail.com")
	if err != nil {
		log.Fatal(err)
	}

	user, err := firebase.AuthClient.GetUserByEmail(
		context.Background(),
		"matiasgozalbez@gmail.com",
	)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("UID: %s", user.UID)
	log.Printf("CLAIMS: %+v", user.CustomClaims)

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

	mux.HandleFunc("/api/mi-socio", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.MiSocio(firebase.Client, firebase.AuthClient)(w, r)
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

	mux.HandleFunc("/api/medicamentos", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.Medicamentos(w, r) // antes decía handlers.Medicamento — con "s" al final
	})

	mux.HandleFunc("/api/afiliados/solicitar-codigo", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.SolicitarCodigo(w, r) // antes decía handlers.Medicamento — con "s" al final
	})

	mux.HandleFunc("/api/afiliados/verificar-codigo", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.VerificarCodigo(w, r)
	})

	mux.HandleFunc("/api/afiliados/crear-password", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.CrearPassword(w, r)
	})

	mux.HandleFunc("/api/admin/notificaciones", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Acá ya le pasas el firebase.Client y firebase.MessagingClient de tu init global
		requireAdmin(handlers.CrearNotificacion(firebase.Client, firebase.MessagingClient))(w, r)
	})

	mux.HandleFunc("/api/admin/listar-socios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		requireAdmin(handlers.ListarSocios(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/notificaciones", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.ObtenerNotificaciones(firebase.Client)(w, r)
	})

	mux.HandleFunc("/api/admin/catalogo-planes", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		requireAdmin(handlers.CrearOActualizarCatalogoPlan(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/obtener-planes", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		requireAdmin(handlers.ObtenerCatalogoPlan(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/socios/beneficios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		requireAdmin(handlers.ActualizarBeneficiosSocio(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/listar-catalogo-planes", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		requireAdmin(handlers.ListarCatalogoPlanes(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/actualizar-socio/", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		requireAdmin(handlers.ActualizarEstadoSocio(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/actualizar-estadoplan/", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		requireAdmin(handlers.ActualizarEstadoPlan(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/planes/detalle", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("🔥 ENTRO A /api/planes/detalle")

		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		handlers.ObtenerCatalogoPlan(firebase.Client)(w, r)
	})

	http.HandleFunc("/api/ping", pingHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Println("Servidor corriendo en el puerto " + port)

	log.Fatal(http.ListenAndServe(":"+port, mux))
}
