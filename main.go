package main

import (
	"aurenbackend/firebase"
	"aurenbackend/handlers"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"aurenbackend/middleware"
	"aurenbackend/utils"

	"github.com/joho/godotenv"
)

func main() {

	if err := godotenv.Load(); err != nil {
		log.Println("Aviso: No se encontro el archivo env")
	}

	handlers.InicializarConfig()
	handlers.InicializarCloudinary()

	firebase.Init()
	InitPG()
	defer ClosePG()

	// Engancha el hub del websocket a registrarAuditoria, así cada evento
	// guardado llega a los paneles abiertos.
	handlers.ConectarNotificador(firebase.MessagingClient)

	mux := http.NewServeMux()
	handlers.AuthClient = firebase.AuthClient
	handlers.PGPool = PG

	// Marca como 'completado' los turnos asignados cuyo horario ya pasó
	// (cada minuto en background). Sin esto podrían cancelarse desde la app
	// y liberar el cupo del mes.
	handlers.IniciarCompletadoAutomatico()
	// DarAdmin queda acá, comentado, como forma de recuperar el acceso si te
	// quedás sin admin. Para usarlo: descomentar, `go build`, correr, y volver
	// a comentar. Importante: el claim viaja en el ID token, así que después de
	// correrlo hay que cerrar sesión y volver a entrar al panel.
	//
	// firebase.DarAdmin("matiasgozalbez@gmail.com")
	// if err != nil {
	// 	log.Println(err)
	// }

	mux.HandleFunc("/api/admin/socios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.CrearSocio())(w, r)
	})

	mux.HandleFunc("/api/vincular-socio", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.VincularSocio(firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/mi-socio", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.MiSocio(firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/verificar-vinculacion", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.VerificarVinculacion(firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/medicamentos", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.Medicamentos(w, r) // antes decía handlers.Medicamento — con "s" al final
	})

	// Frena el pedido masivo de códigos hacia un mismo DNI (spam de mail al
	// socio y consumo de la cuenta de Resend). Un código por minuto: en un
	// flujo normal nunca hace falta más, y llega el código al mail enseguida.
	// Cuenta por DNI, no por IP, porque el abuso es contra un socio y las IPs
	// se comparten detrás de CGNAT.
	limiterCodigos := middleware.NuevoCooldownDNI(time.Minute)

	// El cambio de contraseña martilleado sirve para lo mismo: generar
	// requests sin parar contra un DNI. Mismo cooldown que el envío de código.
	limiterCambioPassword := middleware.NuevoCooldownDNI(time.Minute)

	mux.HandleFunc("/api/afiliados/solicitar-codigo", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		limiterCodigos.Middleware(handlers.SolicitarCodigo)(w, r)
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
		middleware.RequireOperador(handlers.CrearNotificacion(firebase.MessagingClient))(w, r)
	})

	mux.HandleFunc("/api/admin/listar-socios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ListarSocios())(w, r)
	})

	mux.HandleFunc("/api/notificaciones", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.ObtenerNotificaciones(firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/push-token", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.RegistrarPushToken(firebase.AuthClient)(w, r)
	})

	// Tokens FCM de admins/operadores para el push de pedidos nuevos.
	mux.HandleFunc("/api/admin/push-token", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.RegistrarPushTokenAdmin())(w, r)
	})

	mux.HandleFunc("/api/admin/catalogo-planes", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.CrearOActualizarCatalogoPlan())(w, r)
	})

	mux.HandleFunc("/api/admin/obtener-planes", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ObtenerCatalogoPlan())(w, r)
	})

	mux.HandleFunc("/api/admin/socios/beneficios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ActualizarBeneficiosSocio())(w, r)
	})

	mux.HandleFunc("/api/admin/listar-catalogo-planes", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ListarCatalogoPlanes())(w, r)
	})

	mux.HandleFunc("/api/admin/actualizar-socio/", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ActualizarEstadoSocio())(w, r)
	})

	mux.HandleFunc("/api/admin/actualizar-estadoplan/", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ActualizarEstadoPlan())(w, r)
	})

	mux.HandleFunc("/api/planes/detalle", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("🔥 ENTRO A /api/planes/detalle")

		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		handlers.ObtenerCatalogoPlan()(w, r)
	})

	mux.HandleFunc("/api/afiliados/cambiar-password", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		limiterCambioPassword.Middleware(handlers.CambiarPassword)(w, r)
	})

	mux.HandleFunc("/api/admin/crear-medico", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.CrearMedico())(w, r)
	})

	mux.HandleFunc("/api/admin/sugerir-medicos", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.SugerirMedicosCercanos())(w, r)
	})

	mux.HandleFunc("/api/admin/borrar-medico", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.BorrarMedico())(w, r)
	})

	mux.HandleFunc("/api/listar-medicos", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ListarMedicos())(w, r)
	})

	mux.HandleFunc("/api/admin/crear-clinica", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.CrearClinica())(w, r)
	})

	mux.HandleFunc("/api/listar-clinicas", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ListarClinicas())(w, r)
	})

	mux.HandleFunc("/api/admin/editar-clinica", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.EditarClinica())(w, r)
	})

	mux.HandleFunc("/api/admin/borrar-clinica", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.BorrarClinica())(w, r)
	})

	mux.HandleFunc("/api/admin/asignar-clinica", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.AsignarClinica(firebase.MessagingClient))(w, r)
	})

	mux.HandleFunc("/api/crear-turno", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		handlers.CrearTurno(firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/admin/listar-turnos", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ListarTurnos())(w, r)
	})

	mux.HandleFunc("/api/admin/cancelar-turno", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.CancelarTurnoAdmin())(w, r)
	})

	mux.HandleFunc("/api/admin/geocoding-stats", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireAdmin(handlers.GeocodingStatsHandler)(w, r)
	})

	mux.HandleFunc("/api/direcciones/guardar", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.GuardarDireccion()(w, r)
	})

	mux.HandleFunc("/api/direcciones/buscar", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.BuscarDirecciones()(w, r)
	})

	mux.HandleFunc("/api/ciudades/buscar", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.BuscarCiudades()(w, r)
	})

	mux.HandleFunc("/api/admin/asignar-medico", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(
			handlers.AsignarMedico(
				firebase.MessagingClient,
			),
		)(w, r)
	})

	mux.HandleFunc("/api/estudios/subir-imagen", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		handlers.SubirImagenEstudio(firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/admin/estudios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		middleware.RequireOperador(handlers.ListarEstudios())(w, r)
	})

	mux.HandleFunc("/api/admin/estudios/estado", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		middleware.RequireOperador(handlers.CambiarEstadoEstudio())(w, r)
	})

	// ------------------------------------------------------------------
	// Servicios con solicitud: grúa, sepelios y médico a domicilio.
	//
	// Se guardan en la misma tabla "turnos" que los turnos y estudios, con
	// tipo = 'grua' | 'sepelios' | 'domicilio'. Cada uno tiene su propio
	// endpoint de alta porque cada uno exige un plan distinto:
	//
	//   grúa      -> "Auren en Ruta +"
	//   sepelios  -> "Auren Salud" + "Auren Sepelio +"
	//   domicilio -> "Auren Salud"
	//
	// "Seguro de vida" no aparece acá: por ahora es solo una tarjeta en la app,
	// sin solicitud ni form.
	// ------------------------------------------------------------------

	mux.HandleFunc("/api/servicios/grua", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.CrearSolicitudGrua(firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/servicios/sepelios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.CrearSolicitudSepelios(firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/servicios/domicilio", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.CrearSolicitudMedicoDomicilio(firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/servicios/cancelar", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.CancelarMiSolicitud(firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/servicios/cupo", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		handlers.MisSolicitudesCupo(firebase.AuthClient)(w, r)
	})

	mux.HandleFunc("/api/admin/servicios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ListarSolicitudesServicio())(w, r)
	})

	mux.HandleFunc("/api/admin/servicios/estado", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(
			handlers.CambiarEstadoServicio(firebase.MessagingClient),
		)(w, r)
	})

	mux.HandleFunc("/api/mis-turnos", func(w http.ResponseWriter, r *http.Request) {

		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		handlers.MisTurnos(
			firebase.AuthClient,
		)(w, r)
	})

	mux.HandleFunc("/api/mis-turnos/cupo", func(w http.ResponseWriter, r *http.Request) {

		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		handlers.MisTurnosCupo(
			firebase.AuthClient,
		)(w, r)
	})

	mux.HandleFunc("/api/mis-turnos/cancelar", func(w http.ResponseWriter, r *http.Request) {

		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		handlers.CancelarMisTurno(
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
		middleware.RequireAdmin(handlers.CrearAdmin(firebase.AuthClient))(w, r)
	})

	mux.HandleFunc("/api/admin/listar-historial-turnos", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ListarHistorial())(w, r)
	})

	mux.HandleFunc("/api/admin/auditoria", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		middleware.RequireAdmin(handlers.ListarAuditoria())(w, r)
	})

	// Auditoría en vivo. No lleva setCORSHeaders porque un websocket no usa
	// CORS: el Origin se valida adentro del handler, contra la misma lista.
	mux.HandleFunc("/api/ws/admin", func(w http.ResponseWriter, r *http.Request) {
		middleware.RequireAdmin(handlers.WsAuditoria(origenPermitido))(w, r)
	})

	// Avisos de pedidos en vivo. A diferencia del de auditoría, acá también
	// entra el operador: es el que acepta turnos y servicios.
	mux.HandleFunc("/api/ws/pedidos", func(w http.ResponseWriter, r *http.Request) {
		middleware.RequireOperador(handlers.WsPedidos(origenPermitido))(w, r)
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
		middleware.RequireOperador(handlers.EstadisticasSocios)(w, r)
	})

	mux.HandleFunc("/api/admin/importar-socios", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.ImportarSociosCSV())(w, r)
	})

	mux.HandleFunc("/api/admin/enviar-bienvenidas", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.EnviarBienvenidas())(w, r)
	})

	mux.HandleFunc("/api/admin/backfill-socios-conocidos", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		middleware.RequireOperador(handlers.BackfillSociosConocidos())(w, r)
	})

	// El stress test quemaba CPU a propósito (500k iteraciones por request).
	// Público en prod es un DoS, así que queda apagado salvo que lo pidas
	// explícitamente con ENABLE_STRESS_TEST=true.
	if os.Getenv("ENABLE_STRESS_TEST") == "true" {
		mux.HandleFunc("GET /api/test-stress", handlers.HandleTestStress)
	}

	mux.HandleFunc("/api/ping", pingHandler)
	utils.StartMetricsMonitor()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Println("Servidor corriendo en el puerto " + port)

	log.Fatal(http.ListenAndServe(":"+port, mux))

}
