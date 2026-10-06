package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"firebase.google.com/go/v4/auth"
	"github.com/jackc/pgx/v5/pgconn"
)

type TurnoInput struct {
	Especialidad              string `json:"especialidad"`
	Ciudad                    string `json:"ciudad"`
	Direccion                 string `json:"direccion"`
	Motivo                    string `json:"motivo"`
	Modo                      string `json:"modo"` // "geolocalizado" o "profesional"
	NombreProfesionalSugerido string `json:"nombreProfesionalSugerido"`

	// estudios: "consulta" (default) o "estudio"
	Tipo      string `json:"tipo"`
	ImagenURL string `json:"imagenUrl"`

	// Franja horaria preferida por el socio: "mañana" | "tarde" | "noche" o vacío.
	FranjaPreferida string `json:"franjaPreferida"`

	// Si es para un adherente, mandar su DNI. Si va vacío, el turno es para el titular.
	AdherenteDni string `json:"adherenteDni"`
}

// franjasPreferidasValidas son las franjas horarias aceptadas para un turno o estudio.
var franjasPreferidasValidas = map[string]bool{
	"mañana": true,
	"tarde":  true,
	"noche":  true,
}

// cupoMensualPorTipo dice cuántas solicitudes por mes calendario acepta cada
// tipo. Es el número a tocar si cambia la política de un servicio.
//
// Las solicitudes con tipo grua/sepelios/domicilio se crean desde
// /api/servicios/<tipo> (handlers/servicios.go), no desde /api/crear-turno,
// pero el cupo se cuenta contra la misma tabla "turnos", así que el conteo es
// compartido.
var cupoMensualPorTipo = map[string]int{
	"consulta":  1,
	"estudio":   1,
	"grua":      1,
	"sepelios":  1,
	"domicilio": 1,
}

// etiquetaPorTipo es cómo se llama cada tipo en los mensajes al socio.
var etiquetaPorTipo = map[string]string{
	"consulta":  "turno",
	"estudio":   "estudio",
	"grua":      "grúa",
	"sepelios":  "servicio sepelial",
	"domicilio": "médico a domicilio",
}

// esTipoSolicitud dice si el tipo corresponde a un servicio con solicitud
// (grúa, sepelios, médico a domicilio). Esos servicios tienen su propio
// endpoint de alta y su propio gate de plan, así que no entran por
// /api/crear-turno.
func esTipoSolicitud(tipo string) bool {
	_, ok := definicionesPorTipo[tipo]
	return ok
}

// tiposDeServicio devuelve los tipos que se gestionan en el panel de servicios
// (grúa, sepelios y médico a domicilio), ordenados para que las queries sean
// estables. Se usa para:
//   - listarlos en /api/admin/servicios
//   - excluirlos de /api/admin/listar-turnos, que es solo de turnos y estudios
func tiposDeServicio() []string {
	tipos := make([]string, 0, len(definicionesPorTipo))
	for t := range definicionesPorTipo {
		tipos = append(tipos, t)
	}
	sort.Strings(tipos)
	return tipos
}
type Adherente struct {
	Dni        string `json:"dni"`
	Nombre     string `json:"nombre"`
	Apellido   string `json:"apellido"`
	Parentesco string `json:"parentesco"`
}

func CrearTurno(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado, iniciá sesión", http.StatusUnauthorized)
			return
		}

		// Cupo: 1 turno por mes calendario por cuenta (uid) y por tipo.
		// Los cancelados no cuentan. Estudios y consultas tienen cupos separados.
		var input TurnoInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.Tipo = strings.TrimSpace(input.Tipo)
		if input.Tipo == "" {
			input.Tipo = "consulta"
		}

		// Los servicios con solicitud (grúa, sepelios, médico a domicilio) no
		// entran por acá: cada uno tiene su endpoint y su propio gate de plan.
		// Solo pasan por /api/crear-turno los turnos de consulta y estudio.
		if esTipoSolicitud(input.Tipo) {
			http.Error(w, "tipo inválido, ese servicio tiene su propio canal de solicitud", http.StatusBadRequest)
			return
		}
		if input.Tipo != "consulta" && input.Tipo != "estudio" {
			http.Error(w, "tipo inválido, debe ser 'consulta' o 'estudio'", http.StatusBadRequest)
			return
		}

		usoCtx, cancelUso := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelUso()
		usados, errUso := turnosUsadosEnMes(usoCtx, uid, input.Tipo)
		if errUso != nil {
			log.Printf("ERROR verificando cupo del mes para %s (%s): %v", uid, input.Tipo, errUso)
		} else if usados >= cupoMensualPorTipo[input.Tipo] {
			responderSinCupo(w, input.Tipo)
			return
		}

		input.Especialidad = strings.TrimSpace(input.Especialidad)
		input.Ciudad = strings.TrimSpace(input.Ciudad)
		input.Direccion = strings.TrimSpace(input.Direccion)
		input.Modo = strings.TrimSpace(input.Modo)
		input.NombreProfesionalSugerido = strings.TrimSpace(input.NombreProfesionalSugerido)
		input.AdherenteDni = strings.TrimSpace(input.AdherenteDni)
		input.FranjaPreferida = strings.ToLower(strings.TrimSpace(input.FranjaPreferida))
		if input.FranjaPreferida != "" && !franjasPreferidasValidas[input.FranjaPreferida] {
			http.Error(w, "franjaPreferida inválida, debe ser una franja", http.StatusBadRequest)
			return
		}

		if input.Especialidad == "" || input.Ciudad == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}

		if input.Tipo == "estudio" {
			// Un estudio es geolocalizado como una consulta (ciudad + dirección),
			// y el admin le deriva una clínica. Suma la foto obligatoria.
			input.Modo = "geolocalizado"
			if input.Direccion == "" {
				http.Error(w, "falta la dirección para el estudio", http.StatusBadRequest)
				return
			}
			if input.ImagenURL == "" {
				http.Error(w, "falta la imagen del estudio", http.StatusBadRequest)
				return
			}
		} else {
			if input.Modo != "geolocalizado" && input.Modo != "profesional" {
				http.Error(w, "modo inválido, debe ser 'geolocalizado' o 'profesional'", http.StatusBadRequest)
				return
			}

			if input.Modo == "geolocalizado" && input.Direccion == "" {
				http.Error(w, "falta la dirección para buscar por cercanía", http.StatusBadRequest)
				return
			}

			if input.Modo == "profesional" && input.NombreProfesionalSugerido == "" {
				http.Error(w, "falta el nombre del profesional", http.StatusBadRequest)
				return
			}
		}

		// El socio se lee de PostgreSQL (tabla socios).
		// El turno en cambio se guarda en PostgreSQL.

		ctx := context.Background()

		// Buscamos al socio (titular) por su uid de Firebase, NO confiamos en un DNI que mande el front.
		socio, err := leerSocioPorUID(ctx, uid)
		if err != nil {
			http.Error(w, "no se encontró el socio asociado a esta cuenta", http.StatusNotFound)
			return
		}

		if !gateTerminosServicios(ctx, w, socio.DNI) {
			return
		}

		socioData := socio.Data()

		// Los turnos y estudios médicos son servicios exclusivos de Auren Salud.
		// Se valida el estado del socio y el del plan antes de crear el turno.
		acceso := evaluarAccesoAurenSalud(socioData)
		if !acceso.SocioActivo || !acceso.PlanSaludActivo {
			responderSinAccesoSalud(w, acceso)
			return
		}

		socioDni := socio.DNI
		socioNombre := socio.Nombre
		socioApellido := socio.Apellido
		socioEmail := socio.Email
		nombreCompleto := strings.TrimSpace(socioNombre + " " + socioApellido)

		// Datos que van al documento del turno. Por defecto es para el titular.
		beneficiarioDni := socioDni
		beneficiarioNombre := nombreCompleto
		esParaAdherente := false

		if input.AdherenteDni != "" {
			var adherentes []Adherente
			if raw, ok := socioData["adherentes"]; ok {
				// raw viene como []interface{} desde Firestore, lo reconvertimos.
				b, _ := json.Marshal(raw)
				_ = json.Unmarshal(b, &adherentes)
			}

			var encontrado *Adherente
			for i := range adherentes {
				if adherentes[i].Dni == input.AdherenteDni {
					encontrado = &adherentes[i]
					break
				}
			}

			if encontrado == nil {
				http.Error(w, "el adherente indicado no pertenece a tu grupo familiar", http.StatusForbidden)
				return
			}

			esParaAdherente = true
			beneficiarioDni = encontrado.Dni
			beneficiarioNombre = strings.TrimSpace(encontrado.Nombre + " " + encontrado.Apellido)
		}

		// Geocodificamos la dirección del turno (geocodificarDireccion persiste
		// en geo_cache de PG). No bloqueamos el alta: si falla, el turno se crea igual.
		var latTurno, lngTurno float64
		if input.Direccion != "" {
			direccionCompleta := fmt.Sprintf("%s, %s", input.Direccion, input.Ciudad)
			latTurno, lngTurno, err = geocodificarDireccion(direccionCompleta)
			if err != nil {
				log.Printf("WARNING: no se pudo geocodificar dirección del turno: %v", err)
			}
		}

		// El turno ahora vive en PostgreSQL.
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		var turnoID string
		err = PGPool.QueryRow(queryCtx, `
			INSERT INTO turnos (
				uid, socio_dni, socio_email, solicitado_por,
				es_para_adherente, beneficiario_dni, beneficiario_nombre,
				especialidad, ciudad, direccion, lat, lng, motivo, modo,
				nombre_profesional_sugerido, tipo, imagen_url, franja_preferida, estado
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,'pendiente')
			RETURNING id::text`,
			uid,
			socioDni,
			socioEmail,
			nombreCompleto,
			esParaAdherente,
			beneficiarioDni,
			beneficiarioNombre,
			input.Especialidad,
			input.Ciudad,
			input.Direccion,
			latTurno,
			lngTurno,
			input.Motivo,
			input.Modo,
			input.NombreProfesionalSugerido,
			input.Tipo,
			input.ImagenURL,
			input.FranjaPreferida,
		).Scan(&turnoID)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				// Violación del índice único ux_turnos_uid_mes_tipo: doble envío en el mismo mes.
				responderSinCupo(w, input.Tipo)
				return
			}
			log.Printf("ERROR al crear turno en PostgreSQL: %v", err)
			http.Error(w, "error al pedir el turno", http.StatusInternalServerError)
			return
		}

		log.Printf("TURNO CREADO en PostgreSQL id=%s uid=%s tipo=%s", turnoID, uid, input.Tipo)

		// Aviso en vivo al panel: prende el numerito rojo del menú.
		NotificarPedidoNuevo("turnos", input.Tipo)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"id":     turnoID,
		})
	}
}

// turnosUsadosEnMes cuenta los turnos del mes calendario actual para una cuenta
// (uid) y un tipo ('consulta' | 'estudio'), excluyendo los cancelados.
// El mes se renueva el día 1 de cada mes.
func turnosUsadosEnMes(ctx context.Context, uid string, tipo string) (int, error) {
	var usados int
	err := PGPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM turnos
		WHERE uid = $1
		  AND tipo = $2
		  AND estado <> 'cancelado'
		  AND creado_en >= date_trunc('month', CURRENT_TIMESTAMP)`,
		uid, tipo,
	).Scan(&usados)
	return usados, err
}

// responderSinCupo responde el 429 con mensaje claro para la app.
func responderSinCupo(w http.ResponseWriter, tipo string) {
	label, ok := etiquetaPorTipo[tipo]
	if !ok {
		label = "turno"
	}

	// El front mira este código para mostrar "ya usaste tu X de este mes"
	// (SolicitudTurno.tsx / SolicitudTurno*). No cambiarlo sin tocar el front.
	codigo := "TURNO_MES_AGOTADO"
	if esTipoSolicitud(tipo) {
		codigo = "SERVICIO_MES_AGOTADO"
	}

	mensaje := fmt.Sprintf(
		"Ya utilizaste tu %s de este mes. Hay disponibilidad desde el 1º del próximo mes.",
		label,
	)
	if esTipoSolicitud(tipo) {
		mensaje = fmt.Sprintf(
			"Ya utilizaste tu %s de este mes. Hay disponibilidad desde el 1º del próximo mes.",
			label,
		)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "error",
		"codigo":   codigo,
		"mensaje":  mensaje,
		"servicio": tipo,
	})
}

// MisTurnosCupo informa al socio si puede o no pedir un turno en el mes.
func MisTurnosCupo(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado, iniciá sesión", http.StatusUnauthorized)
			return
		}

		tipo := strings.TrimSpace(r.URL.Query().Get("tipo"))
		if tipo == "" {
			tipo = "consulta"
		}
		if tipo != "consulta" && tipo != "estudio" {
			http.Error(w, "tipo inválido", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		usados, err := turnosUsadosEnMes(ctx, uid, tipo)
		if err != nil {
			log.Printf("ERROR leyendo cupo de %s: %v", uid, err)
			http.Error(w, "error leyendo cupo", http.StatusInternalServerError)
			return
		}

		proximoMes := time.Now().AddDate(0, 1, 0)
		inicioProximo := time.Date(proximoMes.Year(), proximoMes.Month(), 1, 0, 0, 0, 0, time.Local)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"usado":      usados,
			"mensual":    cupoMensualPorTipo[tipo],
			"habilitado": usados < cupoMensualPorTipo[tipo],
			"proximoMes": inicioProximo.Format("2006-01-02"),
		})
	}
}

// CancelarMisTurno permite que el socio cancele un turno o estudio propio
// (estado 'pendiente' o 'asignado'), dejando el motivo para que los gestores
// lo vean en el panel. Sin dual-write: solo se actualiza PostgreSQL.
func CancelarMisTurno(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado, iniciá sesión", http.StatusUnauthorized)
			return
		}

		var input struct {
			TurnoID string `json:"turnoId"`
			Motivo  string `json:"motivo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.TurnoID = strings.TrimSpace(input.TurnoID)
		input.Motivo = strings.TrimSpace(input.Motivo)
		if input.TurnoID == "" {
			http.Error(w, "falta turnoId", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var dueno, estadoActual string
		err = PGPool.QueryRow(ctx, `
			SELECT uid, estado FROM turnos WHERE id::text = $1`,
			input.TurnoID,
		).Scan(&dueno, &estadoActual)
		if err != nil {
			http.Error(w, "turno no encontrado", http.StatusNotFound)
			return
		}

		if dueno != uid {
			http.Error(w, "no podés cancelar un turno que no es tuyo", http.StatusForbidden)
			return
		}

		if estadoActual == "cancelado" {
			http.Error(w, "el turno ya está cancelado", http.StatusConflict)
			return
		}
		if estadoActual != "pendiente" && estadoActual != "asignado" {
			http.Error(w, "ese turno ya no se puede cancelar desde la app", http.StatusConflict)
			return
		}

		_, err = PGPool.Exec(ctx, `
			UPDATE turnos SET
				estado = 'cancelado',
				motivo_cancelacion = $2,
				cancelado_por = 'usuario',
				cancelado_en = NOW()
			WHERE id::text = $1`,
			input.TurnoID, input.Motivo,
		)
		if err != nil {
			log.Printf("ERROR cancelando turno %s: %v", input.TurnoID, err)
			http.Error(w, "error al cancelar el turno", http.StatusInternalServerError)
			return
		}

		log.Printf("TURNO CANCELADO por el socio id=%s uid=%s", input.TurnoID, uid)

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}
