package handlers

// handlers/servicios.go
//
// Servicios con solicitud: grúa, sepelios, médico a domicilio y óptica/ortopedia.
//
// Los tres se guardan en la MISMA tabla "turnos" que los turnos y estudios
// médicos, distinguished por la columna "tipo". Reutilizar la tabla es a
// propósito: el cupo mensual, la asignación, la cancelación, el historial, el
// completado automático y el push ya funcionan sobre "turnos" y no hacen falta
// duplicarlos.
//
// "seguro de vida" NO vive acá: por ahora es solo una tarjeta en la app, sin
// solicitud ni form.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"firebase.google.com/go/v4/auth"
	"firebase.google.com/go/v4/messaging"
	"github.com/jackc/pgx/v5/pgconn"
)

// Nombres exactos de los planes. Deben coincidir con handlers/socio.go
// (planColumnas) y con lo que carga el panel admin, porque la comparación es
// por igualdad (con trim + sin distinguir mayúsculas).
const (
	nombrePlanSepelio = "Auren Sepelio +"
	nombrePlanRutaMas = "Auren en Ruta +"
)

// definicionServicio describe un servicio con solicitud: qué plan lo habilita,
// qué es el "tipo de servicio" que elige el socio y cuántos cupos tiene por mes.
type definicionServicio struct {
	// Tipo es el valor que va en turnos.tipo.
	Tipo string

	// Etiqueta es cómo se llama el servicio en los mensajes para el socio.
	Etiqueta string

	// Planes son los planes que el socio necesita, TODOS en estado activo.
	// Un plan solo (Auren Salud) o dos (Auren Salud + Auren Sepelio +).
	Planes []string

	// OpcionesEspecialidad son las opciones del primer select del form.
	// Vacío = el servicio no pide tipo (médico a domicilio), solo motivo.
	OpcionesEspecialidad []string

	// EtiquetaEspecialidad rotula ese select en el form.
	EtiquetaEspecialidad string

	// CopiaEspecialidad es el texto de ayuda del select.
	CopiaEspecialidad string

	// IconoPush y CopiaPush arman el cuerpo de la notificación.
	IconoPush string
	CopiaPush string
}

// Servicios definidos. Los tipos son los mismos que se mandan a la app en la
// ruta (/api/servicios/<tipo>) y los que se filtran en el panel admin.
var (
	ServicioGrua = definicionServicio{
		Tipo:                 "grua",
		Etiqueta:             "grúa",
		Planes:               []string{nombrePlanRutaMas},
		EtiquetaEspecialidad: "Tipo de grúa",
		CopiaEspecialidad:    "Contanos qué tipo de assistance necesitás.",
		OpcionesEspecialidad: []string{"Asistencia mecánica", "Asistencia fiscal", "Remolque"},
		IconoPush:            "🛠️",
		CopiaPush:            "Tu solicitud de grúa fue procesada.",
	}

	ServicioSepelios = definicionServicio{
		Tipo:                 "sepelios",
		Etiqueta:             "servicio sepelial",
		Planes:               []string{nombrePlanSalud, nombrePlanSepelio},
		EtiquetaEspecialidad: "Tipo de servicio",
		CopiaEspecialidad:    "Contanos qué servicio necesitás.",
		OpcionesEspecialidad: []string{"Traslado", "Velatorio", "Sepultura", "Cremación"},
		IconoPush:            "🕊️",
		CopiaPush:            "Tu solicitud de servicio sepelial fue procesada.",
	}

	ServicioMedicoDomicilio = definicionServicio{
		Tipo:                 "domicilio",
		Etiqueta:             "médico a domicilio",
		Planes:               []string{nombrePlanSalud},
		EtiquetaEspecialidad: "",
		CopiaEspecialidad:    "",
		OpcionesEspecialidad: nil,
		IconoPush:            "🏠",
		CopiaPush:            "Tu solicitud de médico a domicilio fue procesada.",
	}

	ServicioOpticaOrtopedia = definicionServicio{
		Tipo:                 "opticaortopedia",
		Etiqueta:             "óptica y ortopedia",
		Planes:               []string{nombrePlanSalud},
		EtiquetaEspecialidad: "Seleccioná una opción",
		CopiaEspecialidad:    "Elegí si necesitás óptica u ortopedia.",
		OpcionesEspecialidad: []string{"Óptica", "Ortopedia"},
		IconoPush:            "👓",
		CopiaPush:            "Tu solicitud de óptica y ortopedia fue procesada.",
	}
)

// definicionesPorTipo indexa los servicios por su tipo, para que los handlers
// genéricos (listar, cambiar estado) puedan resolverlo desde un query param.
var definicionesPorTipo = map[string]definicionServicio{
	ServicioGrua.Tipo:            ServicioGrua,
	ServicioSepelios.Tipo:        ServicioSepelios,
	ServicioMedicoDomicilio.Tipo: ServicioMedicoDomicilio,
	ServicioOpticaOrtopedia.Tipo: ServicioOpticaOrtopedia,
}

// Estados válidos de un servicio con solicitud, desde el panel admin.
// Mismo criterio que los estudios: el socio no edita el estado, lo hace el panel.
var estadosServicioValidos = map[string]bool{
	"pendiente":  true,
	"asignado":   true,
	"en_proceso": true,
	"completado": true,
	"rechazado":  true,
	"cancelado":  true,
}

// ---------------------------------------------------------------------------
// Gate de acceso por plan
// ---------------------------------------------------------------------------

type AccesoServicio struct {
	SocioActivo   bool
	PlanFaltante  string // primer plan requerido que no está activo
	PlanesActivos int    // cuántos de los requeridos están activos
	PlanesPedidos int    // cuántos hacen falta en total
}

// evaluarAccesoServicio indica si el socio puede pedir este servicio.
//
// El socio tiene que estar activo, y encima tener en estado activo TODOS los
// planes que el servicio requiere. Ejemplos:
//
//   - grúa      -> "Auren en Ruta +"                (1 plan)
//   - sepelios  -> "Auren Salud" + "Auren Sepelio +" (2 planes)
//   - domicilio -> "Auren Salud"                    (1 plan)
func evaluarAccesoServicio(data map[string]interface{}, def definicionServicio) *AccesoServicio {
	acceso := &AccesoServicio{
		SocioActivo:   true,
		PlanesPedidos: len(def.Planes),
	}

	estado, _ := data["estado"].(string)
	if estado == "" {
		estado = "activo"
	}
	acceso.SocioActivo = estado == "activo"

	planesRaw, ok := data["planes"].([]interface{})
	if !ok {
		return acceso
	}

	// Juntamos los nombres de los planes que están activos. Los nombres del
	// plan se comparan con trim y sin distinguir mayúsculas, como hace
	// evaluarAccesoAurenSalud.
	activos := make(map[string]bool, len(planesRaw))
	for _, raw := range planesRaw {
		plan, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		nombre, _ := plan["nombre"].(string)
		estadoPlan, _ := plan["estado"].(string)
		if estadoPlan == "" {
			estadoPlan = "activo"
		}
		if estadoPlan == "activo" && strings.TrimSpace(nombre) != "" {
			activos[strings.ToLower(strings.TrimSpace(nombre))] = true
		}
	}

	for _, requerido := range def.Planes {
		if activos[strings.ToLower(strings.TrimSpace(requerido))] {
			acceso.PlanesActivos++
			continue
		}
		// Guardamos el primero que falta: es el que le mostramos al socio.
		if acceso.PlanFaltante == "" {
			acceso.PlanFaltante = requerido
		}
	}

	return acceso
}

// tieneAccesoServicio es el boolean que decide si la solicitud entra.
func (a *AccesoServicio) tieneAccesoServicio() bool {
	return a.SocioActivo && a.PlanesActivos == a.PlanesPedidos
}

// responderSinAccesoServicio responde 403 con código y mensaje claros para la
// app, igual que responderSinAccesoSalud pero parametrizado por servicio.
func responderSinAccesoServicio(w http.ResponseWriter, acceso *AccesoServicio, def definicionServicio) {
	codigo := "PLAN_INACTIVO"
	mensaje := fmt.Sprintf(
		"Tu plan %s se encuentra inactivo", acceso.PlanFaltante,
	)

	if !acceso.SocioActivo {
		codigo = "SOCIO_INACTIVO"
		mensaje = "Usted no se encuentra activo para usar los servicios de auren"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "error",
		"codigo":   codigo,
		"mensaje":  mensaje,
		"servicio": def.Tipo,
	})
}

// ---------------------------------------------------------------------------
// Crear solicitud (socio)
// ---------------------------------------------------------------------------

// SolicitudServicioInput es el body que manda la app. Es basically el mismo
// que el de los turnos, menos el modo de búsqueda por profesional: estos
// servicios siempre son "cerca mío".
type SolicitudServicioInput struct {
	// Para grúa y sepelios: el tipo de servicio elegido de un select.
	// Para médico a domicilio queda vacío.
	Concepto string `json:"concepto"`

	Ciudad    string `json:"ciudad"`
	Direccion string `json:"direccion"`
	Motivo    string `json:"motivo"`

	// Sin franja horaria: estos servicios son de respuesta rápida y no tiene
	// sentido que el socio reserve mañana/tarde/noche. La columna
	// franja_preferida de la tabla sigue existiendo y se escribe vacía; turnos
	// y estudios sí la usan.

	// Si es para un adherente, mandar su DNI. Vacío = titular.
	AdherenteDni string `json:"adherenteDni"`
}

// CrearSolicitudGrua permite a un socio con "Auren en Ruta +" pedir assistance.
func CrearSolicitudGrua(authClient *auth.Client) http.HandlerFunc {
	return crearSolicitud(authClient, ServicioGrua)
}

// CrearSolicitudSepelios requiere "Auren Salud" + "Auren Sepelio +".
func CrearSolicitudSepelios(authClient *auth.Client) http.HandlerFunc {
	return crearSolicitud(authClient, ServicioSepelios)
}

// CrearSolicitudMedicoDomicilio requiere "Auren Salud". No pide tipo de
// servicio, solo lugar y motivo.
func CrearSolicitudMedicoDomicilio(authClient *auth.Client) http.HandlerFunc {
	return crearSolicitud(authClient, ServicioMedicoDomicilio)
}

// CrearSolicitudOpticaOrtopedia requiere "Auren Salud". El socio elige entre
// óptica u ortopedia en el primer select.
func CrearSolicitudOpticaOrtopedia(authClient *auth.Client) http.HandlerFunc {
	return crearSolicitud(authClient, ServicioOpticaOrtopedia)
}

// crearSolicitud es el handler único de los tres servicios. Lo que cambia entre
// uno y otro es la definición (plan requerido, tipo de servicio, cupo).
func crearSolicitud(authClient *auth.Client, def definicionServicio) http.HandlerFunc {
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

		var input SolicitudServicioInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.Concepto = strings.TrimSpace(input.Concepto)
		input.Ciudad = strings.TrimSpace(input.Ciudad)
		input.Direccion = strings.TrimSpace(input.Direccion)
		input.Motivo = strings.TrimSpace(input.Motivo)
		input.AdherenteDni = strings.TrimSpace(input.AdherenteDni)

		// Si el servicio define opciones de tipo de servicio, tiene que venir
		// una y tiene que ser de la lista. Si no define opciones (médico a
		// domicilio) el concepto se ignora.
		if len(def.OpcionesEspecialidad) > 0 {
			if !contiene(def.OpcionesEspecialidad, input.Concepto) {
				http.Error(w, "concepto inválido, elegí una de las opciones", http.StatusBadRequest)
				return
			}
		} else {
			input.Concepto = ""
		}

		// Estos servicios siempre se resuelven en el lugar del socio, así que
		// la dirección es obligatoria: sin ella no hay dónde mandar la grúa ni
		// a quién dejar entrar al médico.
		if input.Ciudad == "" || input.Direccion == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}

		// ---- Cupo mensual (mismo criterio que los turnos) ----
		usoCtx, cancelUso := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancelUso()

		usados, errUso := turnosUsadosEnMes(usoCtx, uid, def.Tipo)
		if errUso != nil {
			log.Printf("ERROR verificando cupo mensual de %s: %v", def.Tipo, errUso)
		} else if usados >= cupoMensualPorTipo[def.Tipo] {
			responderSinCupo(w, def.Tipo)
			return
		}

		// ---- Socio y gate de plan ----
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		socio, err := leerSocioPorUID(ctx, uid)
		if err != nil {
			http.Error(w, "no se encontró el socio asociado a esta cuenta", http.StatusNotFound)
			return
		}

		if !gateTerminosServicios(ctx, w, socio.DNI) {
			return
		}

		socioData := socio.Data()

		acceso := evaluarAccesoServicio(socioData, def)
		if !acceso.tieneAccesoServicio() {
			responderSinAccesoServicio(w, acceso, def)
			return
		}

		// ---- Beneficiario: el titular, o el adherente indicado ----
		beneficiarioDni := socio.DNI
		beneficiarioNombre := strings.TrimSpace(socio.Nombre + " " + socio.Apellido)
		esParaAdherente := false

		if input.AdherenteDni != "" {
			var adherentes []Adherente
			if raw, ok := socioData["adherentes"]; ok {
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

		// ---- Geocodificación (no bloquea el alta si falla) ----
		latitud, longitud, errGeo := geocodificarDireccion(
			fmt.Sprintf("%s, %s", input.Direccion, input.Ciudad),
		)
		if errGeo != nil {
			log.Printf("WARNING: no se pudo geocodificar la dirección de %s: %v", def.Tipo, errGeo)
		}

		// ---- Alta en la tabla turnos ----
		//
		// La columna "especialidad" guarda el tipo de servicio elegido. Es el
		// campo que ya usa el panel para mostrar "qué pidió".
		//
		// "modo" queda fijo en geolocalizado porque no hay búsqueda por
		// profesional en estos servicios.
		// La columna franja_preferida se sigue escribiendo, pero vacía: no
		// sabemos si es NOT NULL sin default, y omitirla rompería el alta.
		var solicitudID string
		err = PGPool.QueryRow(ctx, `
			INSERT INTO turnos (
				uid, socio_dni, socio_email, solicitado_por,
				es_para_adherente, beneficiario_dni, beneficiario_nombre,
				especialidad, ciudad, direccion, lat, lng, motivo, modo,
				nombre_profesional_sugerido, tipo, imagen_url, franja_preferida, estado
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'geolocalizado','',$14,'','','pendiente')
			RETURNING id::text`,
			uid,
			socio.DNI,
			socio.Email,
			strings.TrimSpace(socio.Nombre+" "+socio.Apellido),
			esParaAdherente,
			beneficiarioDni,
			beneficiarioNombre,
			input.Concepto,
			input.Ciudad,
			input.Direccion,
			latitud,
			longitud,
			input.Motivo,
			def.Tipo,
		).Scan(&solicitudID)

		if err != nil {
			// 23505 = índice único ux_turnos_uid_mes_tipo: doble envío en el
			// mismo mes. Para el socio es indistinguible de "cupo agotado".
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				responderSinCupo(w, def.Tipo)
				return
			}
			log.Printf("ERROR creando solicitud de %s: %v", def.Tipo, err)
			http.Error(w, "error al enviar la solicitud", http.StatusInternalServerError)
			return
		}

		log.Printf(
			"SOLICITUD CREADA tipo=%s id=%s uid=%s adherente=%v",
			def.Tipo, solicitudID, uid, esParaAdherente,
		)

		// Aviso en vivo al panel: prende el numerito rojo del menú.
		NotificarPedidoNuevo("servicios", def.Tipo)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"id":     solicitudID,
		})
	}
}

// contiene mira si un string está en una lista de opciones.
func contiene(lista []string, valor string) bool {
	for _, item := range lista {
		if item == valor {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Listar para el panel admin
// ---------------------------------------------------------------------------

// ListarSolicitudesServicio devuelve las solicitudes de UN servicio para el
// panel admin, con la misma forma que devuelve /api/admin/listar-turnos
// (TurnoAdminView), así el panel puede reusar los tipos y los cards.
// RequireAdmin (se aplica como middleware en main.go).
//
// ?tipo=grua|sepelios|domicilio   obligatorio
// ?estado=<estado>                opcional
func ListarSolicitudesServicio() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		// El tipo es opcional: si no viene, el panel pide los tres servicios en
		// una sola llamada (que es como se usa en la práctica). Si viene, se
		// valida contra la lista.
		tipo := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tipo")))

		tipos := []string{}
		if tipo != "" {
			if _, ok := definicionesPorTipo[tipo]; !ok {
				http.Error(w, "tipo de servicio inválido", http.StatusBadRequest)
				return
			}
			tipos = append(tipos, tipo)
		} else {
			tipos = tiposDeServicio()
		}

		// El tipo va siempre como parámetro ($1): nunca se concatena en el SQL.
		// Ojo: el Scan tiene que coincidir con el SELECT columna por columna.
		// Acá no pedimos franja_preferida porque para servicios siempre viene
		// vacía; el campo queda sin llenar en TurnoAdminView.
		query := `
			SELECT id::text, uid, socio_dni, solicitado_por,
				es_para_adherente, beneficiario_dni, beneficiario_nombre,
				COALESCE(tipo,''), COALESCE(especialidad,''), COALESCE(ciudad,''),
				COALESCE(direccion,''), COALESCE(motivo,''), estado, COALESCE(modo,''),
				COALESCE(imagen_url,''),
				COALESCE(motivo_cancelacion,''), COALESCE(cancelado_en::text,''),
				COALESCE(medico_id,''), COALESCE(medico_nombre,''), COALESCE(medico_apellido,''),
				COALESCE(medico_direccion,''), COALESCE(fecha,''), COALESCE(hora,''),
				COALESCE(clinica_id,''), COALESCE(clinica_nombre,''), COALESCE(clinica_direccion,'')
			FROM turnos WHERE tipo = ANY($1)`

		args := []interface{}{tipos}
		estado := strings.TrimSpace(r.URL.Query().Get("estado"))
		if estado != "" {
			query += ` AND estado = $2`
			args = append(args, estado)
		}
		query += ` ORDER BY creado_en DESC`

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := PGPool.Query(ctx, query, args...)
		if err != nil {
			log.Printf("ERROR listando solicitudes de %s: %v", tipo, err)
			http.Error(w, "error leyendo las solicitudes", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		solicitudes := make([]TurnoAdminView, 0)
		for rows.Next() {
			var t TurnoAdminView
			if err := rows.Scan(&t.ID, &t.Uid, &t.SocioDni, &t.SolicitadoPor,
				&t.EsParaAdherente, &t.BeneficiarioDni, &t.BeneficiarioNombre,
				&t.Tipo, &t.Especialidad, &t.Ciudad, &t.Direccion, &t.Motivo, &t.Estado, &t.Modo,
				&t.ImagenURL,
				&t.MotivoCancelacion, &t.CanceladoEn,
				&t.MedicoID, &t.MedicoNombre, &t.MedicoApellido, &t.MedicoDireccion,
				&t.Fecha, &t.Hora,
				&t.ClinicaID, &t.ClinicaNombre, &t.ClinicaDireccion); err != nil {
				continue
			}
			solicitudes = append(solicitudes, t)
		}

		json.NewEncoder(w).Encode(solicitudes)
	}
}

// ---------------------------------------------------------------------------
// Cambiar estado / asignar (panel admin)
// ---------------------------------------------------------------------------

// CambiarEstadoServicio actualiza el estado de una solicitud desde el panel.
// RequireAdmin. Es el equivalente a /api/admin/estudios/estado pero para los
// servicios con solicitud.
//
// Recibe turnoId + estado y opcionalmente profesional, fecha y hora: si viene
// profesional, la solicitud pasa a "asignado" y se guarda quién y cuándo.
func CambiarEstadoServicio(msgClient *messaging.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input struct {
			TurnoID     string `json:"turnoId"`
			Tipo        string `json:"tipo"`
			Estado      string `json:"estado"`
			Profesional string `json:"profesional"`
			Fecha       string `json:"fecha"`
			Hora        string `json:"hora"`
			Motivo      string `json:"motivo"`

			// Para óptica y ortopedia: el id del establecimiento que el panel
			// elige de la lista creada en el menú "Ópticas / Ortopedias".
			PrestadorID int `json:"prestadorId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.TurnoID = strings.TrimSpace(input.TurnoID)
		input.Tipo = strings.ToLower(strings.TrimSpace(input.Tipo))
		input.Estado = strings.TrimSpace(input.Estado)
		input.Profesional = strings.TrimSpace(input.Profesional)
		input.Fecha = strings.TrimSpace(input.Fecha)
		input.Hora = strings.TrimSpace(input.Hora)
		input.Motivo = strings.TrimSpace(input.Motivo)

		def, ok := definicionesPorTipo[input.Tipo]
		if !ok {
			http.Error(w, "tipo de servicio inválido", http.StatusBadRequest)
			return
		}

		if !estadosServicioValidos[input.Estado] {
			http.Error(w, "estado inválido", http.StatusBadRequest)
			return
		}

		if input.TurnoID == "" {
			http.Error(w, "falta turnoId", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Leemos la solicitud actual: necesitamos el uid para el push y para
		// no dejar cambiar de estado algo ya cancelado.
		var uid, estadoActual, concepto, fechaActual, horaActual string
		var beneficiarioNombre, socioEmail, direccionServicio string
		var esParaAdherente bool
		err := PGPool.QueryRow(ctx, `
			SELECT uid, estado, COALESCE(especialidad,''), COALESCE(fecha,''), COALESCE(hora,''),
			       COALESCE(beneficiario_nombre,''), COALESCE(socio_email,''),
			       COALESCE(es_para_adherente,FALSE), COALESCE(direccion,'')
			FROM turnos WHERE id::text = $1 AND tipo = $2`,
			input.TurnoID, def.Tipo,
		).Scan(&uid, &estadoActual, &concepto, &fechaActual, &horaActual,
			&beneficiarioNombre, &socioEmail, &esParaAdherente, &direccionServicio)

		if err != nil {
			http.Error(w, "solicitud no encontrada", http.StatusNotFound)
			return
		}

		if estadoActual == "cancelado" {
			http.Error(w, "esa solicitud ya está cancelada", http.StatusConflict)
			return
		}
		if estadoActual == "completado" {
			http.Error(w, "esa solicitud ya está completada", http.StatusConflict)
			return
		}

		// El panel puede asignar un establecimiento (óptica/ortopedia) en vez
		// de un profesional tipeado. En ese caso los datos que quedan guardados
		// son los del catálogo: nombre del local y dirección completa, para que
		// el socio vea a dónde ir.
		profesionalFinal := input.Profesional
		direccionPrestador := ""
		apellido := ""

		if input.PrestadorID > 0 {
			var pNombre, pDireccion, pCiudad, pProvincia string
			if err := PGPool.QueryRow(ctx, `
				SELECT COALESCE(nombre,''), COALESCE(direccion,''), COALESCE(ciudad,''), COALESCE(provincia,'')
				FROM opticas_ortopedias WHERE id = $1`,
				input.PrestadorID,
			).Scan(&pNombre, &pDireccion, &pCiudad, &pProvincia); err != nil {
				http.Error(w, "óptica/ortopedia no encontrada", http.StatusNotFound)
				return
			}
			profesionalFinal = pNombre
			partes := make([]string, 0, 3)
			for _, p := range []string{pDireccion, pCiudad, pProvincia} {
				if strings.TrimSpace(p) != "" {
					partes = append(partes, strings.TrimSpace(p))
				}
			}
			direccionPrestador = strings.Join(partes, ", ")
		} else {
			// Guardamos el profesional en las columnas que el panel ya conoce
			// (medico_nombre / medico_apellido), así el card del panel los
			// muestra sin campos nuevos. El último token es el apellido.
			if partes := strings.Fields(profesionalFinal); len(partes) > 1 {
				apellido = partes[len(partes)-1]
			}
		}

		// Si viene profesional, la solicitud queda asignada a ese profesional
		// con la fecha y hora que mande el panel. Si no viene, solo cambia el
		// estado y el motivo (rechazo con motivo, por ejemplo).
		estadoFinal := input.Estado
		if profesionalFinal != "" && estadoFinal == "pendiente" {
			estadoFinal = "asignado"
		}

		fechaFinal := fechaActual
		if input.Fecha != "" {
			fechaFinal = input.Fecha
		}
		horaFinal := horaActual
		if input.Hora != "" {
			horaFinal = input.Hora
		}

		if estadoFinal == "cancelado" {
			_, err = PGPool.Exec(ctx, `
				UPDATE turnos SET
					estado = $3,
					motivo_cancelacion = $4,
					cancelado_por = 'admin',
					cancelado_en = NOW()
				WHERE id::text = $1 AND tipo = $2`,
				input.TurnoID, def.Tipo, estadoFinal, input.Motivo,
			)
		} else {
			_, err = PGPool.Exec(ctx, `
				UPDATE turnos SET
					estado = $3,
					medico_nombre = $4,
					medico_apellido = $5,
					fecha = $6,
					hora = $7,
					medico_direccion = $8,
					asignado_en = CASE WHEN $3 = 'asignado' THEN NOW() ELSE asignado_en END
				WHERE id::text = $1 AND tipo = $2`,
				input.TurnoID, def.Tipo, estadoFinal, profesionalFinal, apellido,
				fechaFinal, horaFinal, direccionPrestador,
			)
		}

		if err != nil {
			log.Printf("ERROR actualizando estado de %s: %v", def.Tipo, err)
			http.Error(w, "error actualizando el estado", http.StatusInternalServerError)
			return
		}

		// Un rechazo necesita motivo, aunque sea en el log: sin motivo el socio
		// no sabe qué pasó.
		if estadoFinal == "rechazado" && input.Motivo != "" {
			if _, err := PGPool.Exec(ctx, `
				UPDATE turnos SET motivo = $2 WHERE id::text = $1`,
				input.TurnoID, input.Motivo,
			); err != nil {
				log.Printf("WARNING: no se pudo guardar el motivo de rechazo de %s: %v", def.Tipo, err)
			}
		}

		log.Printf(
			"ESTADO %s: id=%s %s -> %s uid=%s",
			def.Tipo, input.TurnoID, estadoActual, estadoFinal, uid,
		)

		// ---- Push al socio ----
		//
		// Se avisa en los cambios que el socio quiere seguir: asignado,
		// completado y rechazado. Los intermedios (en_proceso) no, para no
		// hacerlo sonar el teléfono al pedo.
		if debeNotificarServicio(estadoActual, estadoFinal) {
			enviarPushEstadoServicio(ctx, msgClient, uid, def, concepto, estadoFinal, fechaFinal, horaFinal, input.Motivo)
		}

		// ---- Email al socio ----
		//
		// Solo en asignado y rechazado: los servicios no mandan mail de
		// completado (definido con el usuario).
		if socioEmail != "" && (estadoFinal == "asignado" || estadoFinal == "rechazado") {
			direccionMail := direccionServicio
			if direccionPrestador != "" {
				direccionMail = direccionPrestador
			}
			err := enviarEmailSolicitud(datosMailSolicitud{
				Estado:       estadoFinal,
				Destinatario: socioEmail,
				Beneficiario: beneficiarioNombre,
				Tipo:         def.Tipo,
				Concepto:     concepto,
				Profesional:  profesionalFinal,
				Direccion:    direccionMail,
				Fecha:        fechaFinal,
				Hora:         horaFinal,
				Motivo:       input.Motivo,
			})
			if err != nil {
				log.Printf("ERROR enviando email de %s a %s: %v", def.Tipo, socioEmail, err)
			} else {
				log.Printf("EMAIL de %s enviado a %s", def.Tipo, socioEmail)
			}
		}

		detalle := detalleBeneficiario(beneficiarioNombre, socioEmail, "", esParaAdherente)
		detalle["tipo"] = def.Tipo
		detalle["estado_anterior"] = estadoActual
		detalle["estado_nuevo"] = estadoFinal
		detalle["fecha"] = fechaFinal
		detalle["hora"] = horaFinal

		registrarAuditoria(r, AccionServicioEstado, "servicio", input.TurnoID, detalle)

		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"estado": estadoFinal,
		})
	}
}

// debeNotificarServicio decide si el cambio de estado amerita un push.
func debeNotificarServicio(desde, hasta string) bool {
	if desde == hasta {
		return false
	}
	switch hasta {
	case "asignado", "completado", "rechazado":
		return true
	}
	return false
}

// enviarPushEstadoServicio manda el push al socio. Nunca corta el flujo: si el
// token no existe o FCM falla, el estado ya quedó guardado y solo se loguea.
func enviarPushEstadoServicio(
	ctx context.Context,
	msgClient *messaging.Client,
	uid string,
	def definicionServicio,
	concepto string,
	estado string,
	fecha string,
	hora string,
	motivo string,
) {
	if uid == "" || msgClient == nil {
		return
	}

	var token string
	if err := PGPool.QueryRow(ctx,
		`SELECT token FROM push_tokens WHERE uid = $1`, uid,
	).Scan(&token); err != nil || token == "" {
		log.Printf("WARNING: no hay push token para el uid de %s", def.Tipo)
		return
	}

	titulo := fmt.Sprintf("%s %s", def.IconoPush, serviceEstadoTitulo[estado])
	cuerpo := def.CopiaPush

	switch estado {
	case "asignado":
		detalle := concepto
		if detalle == "" {
			detalle = strings.ToUpper(def.Etiqueta[:1]) + def.Etiqueta[1:]
		}
		cuerpo = fmt.Sprintf(
			"Tu solicitud de %s (%s) fue asignada para el %s a las %s.",
			detalle, def.Etiqueta, fecha, hora,
		)
	case "completado":
		cuerpo = fmt.Sprintf("Tu solicitud de %s fue completada.", def.Etiqueta)
	case "rechazado":
		if motivo != "" {
			cuerpo = fmt.Sprintf("Tu solicitud de %s no pudo gestionarse: %s", def.Etiqueta, motivo)
		} else {
			cuerpo = fmt.Sprintf("Tu solicitud de %s no pudo gestionarse.", def.Etiqueta)
		}
	}

	push := &messaging.Message{
		Token: token,
		Webpush: &messaging.WebpushConfig{
			Notification: &messaging.WebpushNotification{
				Title: titulo,
				Body:  cuerpo,
				Icon:  "/icon-192.png",
			},
		},
	}

	if _, err := msgClient.Send(ctx, push); err != nil {
		log.Printf("ERROR enviando push de %s al socio: %v", def.Tipo, err)
		return
	}

	log.Printf("PUSH de %s enviado al socio", def.Tipo)
}

// serviceEstadoTitulo es el título del push según el estado nuevo.
var serviceEstadoTitulo = map[string]string{
	"asignado":   "Asignado ✅",
	"completado": "Completado ✅",
	"rechazado":  "No pudo gestionarse",
}

// ---------------------------------------------------------------------------
// Cancelar (socio)
// ---------------------------------------------------------------------------

// CancelarMiSolicitud deja que el socio cancele su propia solicitud mientras
// esté pendiente o asignada, igual que con los turnos. Mismo criterio: los
// cancelados no cuentan para el cupo del mes.
func CancelarMiSolicitud(authClient *auth.Client) http.HandlerFunc {
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
			Tipo    string `json:"tipo"`
			Motivo  string `json:"motivo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.TurnoID = strings.TrimSpace(input.TurnoID)
		input.Tipo = strings.ToLower(strings.TrimSpace(input.Tipo))
		input.Motivo = strings.TrimSpace(input.Motivo)

		def, ok := definicionesPorTipo[input.Tipo]
		if !ok {
			http.Error(w, "tipo de servicio inválido", http.StatusBadRequest)
			return
		}
		if input.TurnoID == "" {
			http.Error(w, "falta turnoId", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var dueno, estadoActual string
		err = PGPool.QueryRow(ctx, `
			SELECT uid, estado FROM turnos WHERE id::text = $1 AND tipo = $2`,
			input.TurnoID, def.Tipo,
		).Scan(&dueno, &estadoActual)

		if err != nil {
			http.Error(w, "solicitud no encontrada", http.StatusNotFound)
			return
		}

		if dueno != uid {
			http.Error(w, "no podés cancelar una solicitud que no es tuya", http.StatusForbidden)
			return
		}

		if estadoActual != "pendiente" && estadoActual != "asignado" {
			http.Error(w, "esa solicitud ya no se puede cancelar desde la app", http.StatusConflict)
			return
		}

		if _, err := PGPool.Exec(ctx, `
			UPDATE turnos SET
				estado = 'cancelado',
				motivo_cancelacion = $3,
				cancelado_por = 'usuario',
				cancelado_en = NOW()
			WHERE id::text = $1 AND tipo = $2`,
			input.TurnoID, def.Tipo, input.Motivo,
		); err != nil {
			log.Printf("ERROR cancelando solicitud de %s: %v", def.Tipo, err)
			http.Error(w, "error al cancelar la solicitud", http.StatusInternalServerError)
			return
		}

		log.Printf("SOLICITUD CANCELADA por el socio tipo=%s id=%s", def.Tipo, input.TurnoID)

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

// ---------------------------------------------------------------------------
// Cupo del mes (app)
// ---------------------------------------------------------------------------

// MisSolicitudesCupo informa al socio si le queda cupo de este servicio.
func MisSolicitudesCupo(authClient *auth.Client) http.HandlerFunc {
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

		tipo := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tipo")))
		def, ok := definicionesPorTipo[tipo]
		if !ok {
			http.Error(w, "tipo de servicio inválido", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		usados, err := turnosUsadosEnMes(ctx, uid, def.Tipo)
		if err != nil {
			log.Printf("ERROR leyendo cupo de %s: %v", def.Tipo, err)
			http.Error(w, "error leyendo cupo", http.StatusInternalServerError)
			return
		}

		proximoMes := time.Now().AddDate(0, 1, 0)
		inicioProximo := time.Date(proximoMes.Year(), proximoMes.Month(), 1, 0, 0, 0, 0, time.Local)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"usado":      usados,
			"mensual":    cupoMensualPorTipo[def.Tipo],
			"habilitado": usados < cupoMensualPorTipo[def.Tipo],
			"proximoMes": inicioProximo.Format("2006-01-02"),
		})
	}
}
