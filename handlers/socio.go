package handlers

// handlers/socios.go

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"firebase.google.com/go/v4/auth"
)

type HistorialAdminView struct {
	ID                 string `json:"id"`
	TurnoID            string `json:"turnoId"`
	SocioDni           string `json:"socioDni"`
	BeneficiarioDni    string `json:"beneficiarioDni"`
	BeneficiarioNombre string `json:"beneficiarioNombre"`
	EsParaAdherente    bool   `json:"esParaAdherente"`
	Especialidad       string `json:"especialidad"`
	Ciudad             string `json:"ciudad"`
	Direccion          string `json:"direccion"`
	MedicoNombre       string `json:"medicoNombre"`
	MedicoApellido     string `json:"medicoApellido"`
	MedicoDireccion    string `json:"medicoDireccion"`
	ClinicaNombre      string `json:"clinicaNombre"`
	Fecha              string `json:"fecha"`
	Hora               string `json:"hora"`
	Estado             string `json:"estado"`
}

type AdherenteInput struct {
	Relacion string `json:"relacion"`
	Nombre   string `json:"nombre"`
	Apellido string `json:"apellido"`
	DNI      string `json:"dni"`
	Edad     string `json:"edad"`
}

type PlanSocio struct {
	Nombre string `json:"nombre"`
	Estado string `json:"estado"` // "activo", "inactivo", etc.
}

type SocioInput struct {
	DNI                   string           `json:"dni"`
	Nombre                string           `json:"nombre"`
	Apellido              string           `json:"apellido"`
	Email                 string           `json:"email"`
	Edad                  string           `json:"edad"`
	Provincia             string           `json:"provincia"`
	Ciudad                string           `json:"ciudad"`
	Direccion             string           `json:"direccion"`
	MetodoPago            string           `json:"metodoPago"`
	CBU                   string           `json:"cbu"`
	TarjetaUltimosDigitos string           `json:"tarjetaUltimosDigitos"` // solo los últimos 4, nunca el número completo
	TarjetaVencimiento    string           `json:"tarjetaVencimiento"`
	Planes                []PlanSocio      `json:"planes"`
	Estado                string           `json:"estado"`
	Adherentes            []AdherenteInput `json:"adherentes"`
}

var planColumnas = []string{"Auren Salud", "Auren Sepelio +", "Auren en Ruta", "Auren en Ruta +"}

var (
	reDNI    = regexp.MustCompile(`^\d{7,8}$`)
	reNombre = regexp.MustCompile(`^[A-Za-zÁÉÍÓÚáéíóúÑñÜü\s'-]{2,50}$`)
	reEmail  = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	reCBU    = regexp.MustCompile(`^\d{22}$`)
)

func verifyIDToken(r *http.Request, authClient *auth.Client) (string, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return "", errors.New("sin token")
	}
	idToken := strings.TrimPrefix(header, "Bearer ")

	token, err := authClient.VerifyIDToken(context.Background(), idToken)
	if err != nil {
		return "", err
	}
	return token.UID, nil
}

func CrearSocio() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input SocioInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if input.DNI == "" || input.Nombre == "" || input.Apellido == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}
		if !reDNI.MatchString(input.DNI) {
			http.Error(w, "DNI inválido", http.StatusBadRequest)
			return
		}

		if !reNombre.MatchString(input.Nombre) {
			http.Error(w, "nombre inválido", http.StatusBadRequest)
			return
		}

		if !reNombre.MatchString(input.Apellido) {
			http.Error(w, "apellido inválido", http.StatusBadRequest)
			return
		}

		if input.Email != "" && !reEmail.MatchString(input.Email) {
			http.Error(w, "email inválido", http.StatusBadRequest)
			return
		}

		if input.CBU != "" && !reCBU.MatchString(input.CBU) {
			http.Error(w, "CBU inválido", http.StatusBadRequest)
			return
		}
		if len(input.Planes) > 4 {
			http.Error(w, "máximo 4 planes por socio", http.StatusBadRequest)
			return
		}
		if len(input.Adherentes) > 10 {
			http.Error(w, "máximo 10 adherentes por titular", http.StatusBadRequest)
			return
		}
		if input.Estado == "" {
			input.Estado = "activo"
		}

		// Validamos cada adherente antes de guardar.
		for _, a := range input.Adherentes {
			if a.Nombre == "" || a.Apellido == "" || a.DNI == "" {
				http.Error(w, "faltan datos del adherente", http.StatusBadRequest)
				return
			}

			if !reDNI.MatchString(a.DNI) {
				http.Error(w, "DNI de adherente inválido", http.StatusBadRequest)
				return
			}

			if !reNombre.MatchString(a.Nombre) {
				http.Error(w, "nombre de adherente inválido", http.StatusBadRequest)
				return
			}

			if !reNombre.MatchString(a.Apellido) {
				http.Error(w, "apellido de adherente inválido", http.StatusBadRequest)
				return
			}

			edad, err := strconv.Atoi(a.Edad)
			if err != nil || edad < 1 || edad > 120 {
				http.Error(w, "edad de adherente inválida", http.StatusBadRequest)
				return
			}
		}

		ctx := context.Background()

		// ¿Ya existe ese DNI?
		if _, err := leerSocioPorDNI(ctx, input.DNI); err == nil {
			http.Error(w, "el socio ya existe", http.StatusConflict)
			return
		}

		emailToSave := input.Email
		if emailToSave != "" {
			emailToSave = strings.ToLower(strings.TrimSpace(input.Email))
			var yaExiste bool
			if err := PGPool.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM socios WHERE email = $1)`, emailToSave,
			).Scan(&yaExiste); err == nil && yaExiste {
				http.Error(w, "el email ya está registrado", http.StatusConflict)
				return
			}
		}

		planesData, err := jsonbSociosPlanes(input.Planes)
		if err != nil {
			http.Error(w, "error guardando socio", http.StatusInternalServerError)
			return
		}
		adherentesData, err := jsonbSociosAdherentes(input.Adherentes)
		if err != nil {
			http.Error(w, "error guardando socio", http.StatusInternalServerError)
			return
		}

		_, err = PGPool.Exec(ctx, `
			INSERT INTO socios (
				dni, nombre, apellido, email, edad, provincia, ciudad, direccion,
				metodo_pago, cbu, tarjeta_ultimos_digitos, tarjeta_vencimiento,
				planes, estado, adherentes, uid
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NULL)`,
			input.DNI,
			input.Nombre,
			input.Apellido,
			emailToSave,
			input.Edad,
			input.Provincia,
			input.Ciudad,
			input.Direccion,
			input.MetodoPago,
			input.CBU,
			input.TarjetaUltimosDigitos,
			input.TarjetaVencimiento,
			planesData,
			input.Estado,
			adherentesData,
		)
		if err != nil {
			log.Printf("ERROR guardando socio %s: %v", input.DNI, err)
			http.Error(w, "error guardando socio", http.StatusInternalServerError)
			return
		}

		if emailToSave != "" {
			if _, err := PGPool.Exec(ctx,
				`INSERT INTO socios_conocidos (dni, email) VALUES ($1, $2) ON CONFLICT (dni) DO NOTHING`,
				input.DNI, emailToSave); err != nil {
				log.Printf("ERROR registrando socio %s en socios_conocidos: %v", input.DNI, err)
			}
		}

		registrarAuditoria(r, AccionSocioCrear, "socio", input.DNI, map[string]any{
			"estado": input.Estado,
		})

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func VincularSocio(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado, iniciá sesión", http.StatusUnauthorized)
			return
		}

		var input SocioInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		socio, err := leerSocioPorDNI(ctx, input.DNI)
		if err != nil {
			http.Error(w, "ese DNI no pertenece a ningún socio", http.StatusNotFound)
			return
		}

		if socio.UID != nil {
			http.Error(w, "este DNI ya tiene una cuenta vinculada", http.StatusConflict)
			return
		}

		mailRegistrado := socio.Email

		usuarioAuth, err := authClient.GetUser(ctx, uid)
		if err != nil {
			http.Error(w, "error verificando cuenta", http.StatusInternalServerError)
			return
		}

		if !strings.EqualFold(strings.TrimSpace(usuarioAuth.Email), strings.TrimSpace(mailRegistrado)) {
			http.Error(w, "el mail de tu cuenta no coincide con el registrado para este DNI", http.StatusForbidden)
			return
		}

		_, err = PGPool.Exec(ctx,
			`UPDATE socios SET uid = $2, actualizado_en = now() WHERE dni = $1`,
			input.DNI, uid,
		)
		if err != nil {
			http.Error(w, "error vinculando cuenta", http.StatusInternalServerError)
			return
		}

		// Si el socio aceptó los términos justo antes de vincularse, la
		// aceptación quedó sin uid (aún no existía); se completa ahora.
		marcarUIDAceptacion(ctx, input.DNI, uid)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func VerificarVinculacion(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		ctx := context.Background()
		var vinculado bool
		_ = PGPool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM socios WHERE uid = $1)`, uid,
		).Scan(&vinculado)

		// terminosAceptados le dice al front si este socio está al día con la
		// versión vigente de los T&C. Sin socio vinculado no importa: el
		// login va a /vincular-dni igual, que tiene prioridad.
		terminosAceptados := true
		if vinculado {
			socio, err := leerSocioPorUID(ctx, uid)
			if err == nil {
				terminosAceptados, _ = TerminosAceptadosParaDNI(ctx, socio.DNI)
			}
		}

		json.NewEncoder(w).Encode(map[string]bool{
			"vinculado":         vinculado,
			"terminosAceptados": terminosAceptados,
		})
	}
}

func MiSocio(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		ctx := context.Background()
		socio, err := leerSocioPorUID(ctx, uid)
		if err != nil {
			http.Error(w, "socio no encontrado", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(socio.Data())
	}
}

// socioPublico arma el mapa que espera el panel, preservando los nombres de
// campos que devolvía Firestore. completo agrega provincia/ciudad/direccion
// (solo se usan en la vista de detalle ?id=).
func socioPublico(s *SocioRecord, completo bool) map[string]interface{} {
	socio := map[string]interface{}{
		"id":         s.DNI,
		"uid":        s.Data()["uid"],
		"nombre":     s.Nombre,
		"apellido":   s.Apellido,
		"email":      s.Email,
		"dni":        s.DNI,
		"edad":       s.Edad,
		"planes":     s.Data()["planes"],
		"estado":     s.Estado,
		"adherentes": s.Data()["adherentes"],
		"beneficios": s.Data()["beneficios"],
	}
	if completo {
		socio["provincia"] = s.Provincia
		socio["ciudad"] = s.Ciudad
		socio["direccion"] = s.Direccion
	}
	return socio
}

func ListarSocios() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()

		// Si se pasa ?id=<dni>, devolver solo ese socio (1 lectura)
		if id := r.URL.Query().Get("id"); id != "" {
			socio, err := leerSocioPorDNI(ctx, id)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"socios": []interface{}{}, "nextCursor": "", "hasMore": false,
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"socios":     []interface{}{socioPublico(socio, true)},
				"nextCursor": "",
				"hasMore":    false,
			})
			return
		}

		// limit: cuántos socios traer por página (default 20)
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
				limit = parsed
			}
		}

		// cursor: el DNI del último socio de la página anterior
		cursor := r.URL.Query().Get("cursor")

		query := "SELECT " + columnasSocio + " FROM socios"
		var args []interface{}

		if cursor != "" {
			query += " WHERE dni > $1"
			args = append(args, cursor)
		}

		query += " ORDER BY dni ASC LIMIT $" + strconv.Itoa(len(args)+1)
		args = append(args, limit)

		rows, err := PGPool.Query(ctx, query, args...)
		if err != nil {
			http.Error(w, "error obteniendo socios", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var socios []map[string]interface{}
		var ultimoDNI string
		for rows.Next() {
			s, err := scanSocio(rows)
			if err != nil {
				continue
			}
			socios = append(socios, socioPublico(s, false))
			ultimoDNI = s.DNI
		}

		hasMore := len(socios) == limit
		nextCursor := ""
		if hasMore {
			nextCursor = ultimoDNI
		}

		respuesta := map[string]interface{}{
			"socios":     socios,
			"nextCursor": nextCursor,
			"hasMore":    hasMore,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(respuesta)
	}
}

func ActualizarEstadoSocio() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input struct {
			ID         string                   `json:"id"`
			Estado     string                   `json:"estado"`
			Adherentes []map[string]interface{} `json:"adherentes"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if input.ID == "" {
			http.Error(w, "falta el ID del socio", http.StatusBadRequest)
			return
		}

		if input.Estado == "" {
			input.Estado = "activo"
		}

		ctx := context.Background()

		// Leemos el estado ACTUAL antes de pisarlo, para saber qué restar del contador
		socioActual, err := leerSocioPorDNI(ctx, input.ID)
		if err != nil {
			http.Error(w, "error obteniendo socio actual", http.StatusInternalServerError)
			return
		}
		estadoAnterior := socioActual.Estado

		query := `UPDATE socios SET estado = $2, actualizado_en = now()`
		var args []interface{} = []interface{}{input.ID, input.Estado}
		if input.Adherentes != nil {
			adherentesJSON, err := json.Marshal(input.Adherentes)
			if err != nil {
				http.Error(w, "adherentes inválidos", http.StatusBadRequest)
				return
			}
			query += `, adherentes = $3`
			args = append(args, adherentesJSON)
		}
		query += ` WHERE dni = $1`

		_, err = PGPool.Exec(ctx, query, args...)
		if err != nil {
			http.Error(w, "error al actualizar el socio: "+err.Error(), http.StatusInternalServerError)
			return
		}

		registrarAuditoria(r, AccionSocioActualizar, "socio", input.ID, map[string]any{
			"estado_anterior": estadoAnterior,
			"estado_nuevo":    input.Estado,
		})

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func ActualizarEstadoPlan() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		// Extraer ID desde la URL
		partes := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(partes) < 4 {
			http.Error(w, "ID de socio inválido", http.StatusBadRequest)
			return
		}

		socioID := partes[len(partes)-1]

		var input struct {
			Plan   string `json:"plan"`
			Estado string `json:"estado"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido: "+err.Error(), http.StatusBadRequest)
			return
		}

		if input.Plan == "" {
			http.Error(w, "falta el plan", http.StatusBadRequest)
			return
		}

		if input.Estado == "" {
			http.Error(w, "falta el estado", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		socio, err := leerSocioPorDNI(ctx, socioID)
		if err != nil {
			http.Error(w, "socio no encontrado", http.StatusNotFound)
			return
		}

		var planes []map[string]interface{}
		if err := json.Unmarshal(socio.Planes, &planes); err != nil {
			http.Error(w, "el socio no tiene planes válidos", http.StatusInternalServerError)
			return
		}

		encontrado := false
		for _, plan := range planes {
			nombre, _ := plan["nombre"].(string)
			if nombre == input.Plan {
				plan["estado"] = input.Estado
				encontrado = true
				break
			}
		}

		if !encontrado {
			http.Error(w, "plan no encontrado", http.StatusNotFound)
			return
		}

		planesJSON, err := json.Marshal(planes)
		if err != nil {
			http.Error(w, "error actualizando plan: "+err.Error(), http.StatusInternalServerError)
			return
		}

		_, err = PGPool.Exec(ctx,
			`UPDATE socios SET planes = $2, actualizado_en = now() WHERE dni = $1`,
			socioID, planesJSON,
		)
		if err != nil {
			http.Error(w, "error actualizando plan: "+err.Error(), http.StatusInternalServerError)
			return
		}

		registrarAuditoria(r, AccionSocioPlanEstado, "socio", socioID, map[string]any{
			"plan":   input.Plan,
			"estado": input.Estado,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	}
}

func ListarHistorial() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := PGPool.Query(ctx, `
			SELECT id::text, COALESCE(turno_id::text,''), COALESCE(uid,''),
				   COALESCE(especialidad,''), COALESCE(medico_nombre,''),
				   COALESCE(medico_apellido,''), COALESCE(fecha,''), COALESCE(hora,''),
				   COALESCE(clinica_nombre,'')
			FROM historial_turnos
			ORDER BY creado_en DESC`)
		if err != nil {
			log.Printf("ERROR LEYENDO HISTORIAL: %v", err)
			http.Error(w, "error leyendo historial: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		historial := make([]HistorialAdminView, 0)
		for rows.Next() {
			var h HistorialAdminView
			var uidIgnored string
			if err := rows.Scan(&h.ID, &h.TurnoID, &uidIgnored, &h.Especialidad,
				&h.MedicoNombre, &h.MedicoApellido, &h.Fecha, &h.Hora, &h.ClinicaNombre); err != nil {
				continue
			}
			historial = append(historial, h)
		}

		json.NewEncoder(w).Encode(historial)
	}
}

func ImportarSociosCSV() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		file, _, err := r.FormFile("archivo")
		if err != nil {
			http.Error(w, "archivo requerido (campo 'archivo')", http.StatusBadRequest)
			return
		}
		defer file.Close()

		reader := csv.NewReader(file)
		reader.LazyQuotes = true

		headers, err := reader.Read()
		if err != nil {
			http.Error(w, "no se pudo leer el header del CSV", http.StatusBadRequest)
			return
		}
		headers[0] = strings.TrimPrefix(headers[0], "\ufeff") // BOM de Wix

		colIdx := make(map[string]int)
		for i, h := range headers {
			colIdx[h] = i
		}

		ctx := context.Background()

		procesados, omitidos := 0, 0
		nuevosActivos, nuevosInactivos := 0, 0

		var filas []socioCSVRow
		var procesadosDNIs []string
		var procesadosEmails []string

		for {
			row, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				omitidos++
				continue
			}

			get := func(col string) string {
				if idx, ok := colIdx[col]; ok && idx < len(row) {
					return strings.TrimSpace(row[idx])
				}
				return ""
			}

			dni := get("DNI")
			nombre := get("Nombre")
			apellido := get("Apellido")
			email := strings.ToLower(get("Email"))

			if !reDNI.MatchString(dni) || !reNombre.MatchString(nombre) || !reNombre.MatchString(apellido) {
				log.Printf("fila omitida, datos inválidos: dni=%s nombre=%s", dni, nombre)
				omitidos++
				continue
			}
			if email != "" && !reEmail.MatchString(email) {
				email = "" // preferí guardarlo vacío antes que romper el import por un mail mal cargado en Wix
			}

			var planes []map[string]interface{}
			for _, plan := range planColumnas {
				if get(plan) == "true" {
					planes = append(planes, map[string]interface{}{
						"nombre": plan,
						"estado": "activo",
					})
				}
			}

			estado := mapearEstado(get("Estado de Afiliación"))

			filas = append(filas, socioCSVRow{
				dni:       dni,
				nombre:    nombre,
				apellido:  apellido,
				email:     email,
				telefono:  get("Telefono"),
				direccion: get("Dirección"),
				planes:    planes,
				estado:    estado,
			})

			procesados++
			procesadosDNIs = append(procesadosDNIs, dni)
			procesadosEmails = append(procesadosEmails, email)
			if estado == "activo" {
				nuevosActivos++
			} else if estado == "inactivo" {
				nuevosInactivos++
			}
		}

		if len(filas) > 0 {
			if err := upsertSociosCSV(ctx, filas); err != nil {
				http.Error(w, "error guardando socios en la base", http.StatusInternalServerError)
				return
			}
		}

		nuevos := 0
		if len(procesadosDNIs) > 0 {
			conocidos, err := dnisConocidos(ctx, procesadosDNIs)
			if err != nil {
				log.Printf("ERROR consultando socios_conocidos durante import: %v", err)
			} else {
				pendientes := make([][]interface{}, 0, len(procesadosDNIs))
				for i, dni := range procesadosDNIs {
					if !conocidos[dni] && procesadosEmails[i] != "" {
						pendientes = append(pendientes, []interface{}{dni, procesadosEmails[i]})
					}
				}
				if n, err := insertConocidos(ctx, []string{"dni", "email"}, pendientes); err != nil {
					log.Printf("ERROR registrando nuevos en socios_conocidos: %v", err)
				} else {
					nuevos = n
				}
			}
		}

		// Nota: esto es un approach simplificado — recalcula sobre lo importado,
		// no diferencia altas nuevas de actualizaciones de socios que ya existían
		// con otro estado. Para stats 100% exactos habría que leer el estado previo
		// de cada uno (lo cual reintroduce lecturas). A definir si les alcanza así.

		registrarAuditoria(r, AccionSocioImportarCSV, "socio", "", map[string]any{
			"procesados": procesados,
			"omitidos":   omitidos,
			"nuevos":     nuevos,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "ok",
			"procesados": procesados,
			"omitidos":   omitidos,
			"nuevos":     nuevos,
		})
	}
}

func mapearEstado(estadoWix string) string {
	switch strings.ToLower(strings.TrimSpace(estadoWix)) {
	case "activo", "activa":
		return "activo"
	case "inactivo", "inactiva":
		return "inactivo"
	case "suspendido", "suspendida":
		return "suspendido"
	default:
		return "activo" // TODO: default hasta que confirmes los valores reales que exporta Wix
	}
}
