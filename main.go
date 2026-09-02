package main

import (
	"aurenbackend/firebase"
	"aurenbackend/handlers"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"aurenbackend/middleware"
	"aurenbackend/utils"

	"github.com/joho/godotenv"
)

func main() {

	if err := godotenv.Load(); err != nil {
		log.Println("Aviso: No se encontro el archivo env")
	}

	if err := godotenv.Load(); err != nil {
		log.Println("Aviso: No se encontro el archivo env")
	}

	handlers.InicializarConfig()

	firebase.Init()
	mux := http.NewServeMux()
	handlers.FirestoreClient = firebase.Client
	handlers.AuthClient = firebase.AuthClient
	/*
		if err := handlers.ReconstruirStats(firebase.Client); err != nil {
			log.Fatal(err)
		}
	*/
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
		middleware.RequireAdmin(handlers.CrearSocio(firebase.Client))(w, r)
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
		middleware.RequireAdmin(handlers.CrearNotificacion(firebase.Client, firebase.MessagingClient))(w, r)
	})

	mux.HandleFunc("/api/admin/listar-socios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.ListarSocios(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/notificaciones", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.ObtenerNotificaciones(firebase.Client, firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/admin/catalogo-planes", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.CrearOActualizarCatalogoPlan(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/obtener-planes", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.ObtenerCatalogoPlan(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/socios/beneficios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.ActualizarBeneficiosSocio(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/listar-catalogo-planes", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.ListarCatalogoPlanes(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/actualizar-socio/", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.ActualizarEstadoSocio(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/actualizar-estadoplan/", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.ActualizarEstadoPlan(firebase.Client))(w, r)
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

	mux.HandleFunc("/api/afiliados/cambiar-password", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.CambiarPassword(w, r)
	})

	mux.HandleFunc("/api/admin/crear-medico", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.CrearMedico(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/listar-medicos", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.ListarMedicos(firebase.Client, firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/crear-turno", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		handlers.CrearTurno(firebase.Client, firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/admin/listar-turnos", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.ListarTurnos(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/asignar-medico", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(
			handlers.AsignarMedico(
				firebase.Client,
				firebase.MessagingClient,
			),
		)(w, r)
	})

	mux.HandleFunc("/api/mis-turnos", func(w http.ResponseWriter, r *http.Request) {

		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		handlers.MisTurnos(
			firebase.Client,
			firebase.AuthClient,
		)(w, r)
	})

	mux.HandleFunc("/api/admin/otorgar-admin", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.OtorgarAdmin(firebase.AuthClient))(w, r)
	})

	mux.HandleFunc("/api/admin/revocar-admin", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.RevocarAdmin(firebase.AuthClient))(w, r)
	})

	mux.HandleFunc("/api/admin/crear-admin", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.CrearAdmin(firebase.Client, firebase.AuthClient))(w, r)
	})

	mux.HandleFunc("/api/admin/listar-historial-turnos", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.ListarHistorial(firebase.Client))(w, r)
	})

	mux.HandleFunc("/api/admin/servidor/metricas", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		middleware.RequireAdmin(handlers.MetricasServidor)(w, r)
	})

	mux.HandleFunc("/api/admin/servidor/logs", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		middleware.RequireAdmin(handlers.LogsServidor)(w, r)
	})

	mux.HandleFunc("/api/admin/listar-estadisticas", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.EstadisticasSocios)(w, r)
	})

	mux.HandleFunc("GET /api/test-stress", handlers.HandleTestStress)

	http.HandleFunc("/api/ping", pingHandler)
	utils.StartMetricsMonitor()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Println("Servidor corriendo en el puerto " + port)

	log.Fatal(http.ListenAndServe(":"+port, mux))

}
