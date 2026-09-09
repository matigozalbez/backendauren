package handlers

// handlers/socios.go

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"log"
	"strconv"

	"cloud.google.com/go/firestore"
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

func CrearSocio(fsClient *firestore.Client) http.HandlerFunc {
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

		// Convertimos adherentes a []interface{} para que Firestore lo
		// guarde como array de mapas
		adherentesData := make([]interface{}, 0, len(input.Adherentes))
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

			adherentesData = append(adherentesData, map[string]interface{}{
				"relacion": a.Relacion,
				"nombre":   a.Nombre,
				"apellido": a.Apellido,
				"dni":      a.DNI,
				"edad":     a.Edad,
			})
		}

		planesData := make([]interface{}, 0, len(input.Planes))
		for _, p := range input.Planes {
			estadoPlan := p.Estado
			if estadoPlan == "" {
				estadoPlan = "activo" // Por defecto activo si no se envía
			}
			planesData = append(planesData, map[string]interface{}{
				"nombre": p.Nombre,
				"estado": estadoPlan,
			})
		}

		ctx := context.Background()

		doc, err := fsClient.Collection("socios").Doc(input.DNI).Get(ctx)

		if err != nil && status.Code(err) != codes.NotFound {
			http.Error(w, "error verificando socio", http.StatusInternalServerError)
			return
		}

		if err == nil && doc.Exists() {
			http.Error(w, "el socio ya existe", http.StatusConflict)
			return
		}

		emailToSave := input.Email
		if input.Email != "" {
			emailToSave = strings.ToLower(strings.TrimSpace(input.Email))
			iter := fsClient.Collection("socios").Where("email", "==", emailToSave).Limit(1).Documents(ctx)
			docs, err := iter.GetAll()
			if err != nil {
				http.Error(w, "error verificando email", http.StatusInternalServerError)
				return
			}
			if len(docs) > 0 {
				http.Error(w, "el email ya está registrado", http.StatusConflict)
				return
			}
		}

		_, err = fsClient.Collection("socios").Doc(input.DNI).Set(ctx, map[string]interface{}{
			"dni":                   input.DNI,
			"nombre":                input.Nombre,
			"apellido":              input.Apellido,
			"email":                 emailToSave,
			"edad":                  input.Edad,
			"provincia":             input.Provincia,
			"ciudad":                input.Ciudad,
			"direccion":             input.Direccion,
			"metodoPago":            input.MetodoPago,
			"cbu":                   input.CBU,
			"tarjetaUltimosDigitos": input.TarjetaUltimosDigitos,
			"tarjetaVencimiento":    input.TarjetaVencimiento,
			"planes":                planesData,
			"estado":                input.Estado,
			"adherentes":            adherentesData,
			"uid":                   nil,
		})
		if err != nil {
			http.Error(w, "error guardando socio", http.StatusInternalServerError)
			return
		}

		stats, errStats := leerStats()
		if errStats == nil {
			stats.TotalSocios++
			switch input.Estado {
			case "activo":
				stats.Activos++
			case "inactivo":
				stats.Inactivos++
			case "suspendido":
				stats.Suspendidos++
			}
			stats.TotalAdherentes += len(input.Adherentes)
			guardarStats(stats)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func VincularSocio(fsClient *firestore.Client, authClient *auth.Client) http.HandlerFunc {
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
		doc, err := fsClient.Collection("socios").Doc(input.DNI).Get(ctx)
		if err != nil || !doc.Exists() {
			http.Error(w, "ese DNI no pertenece a ningún socio", http.StatusNotFound)
			return
		}

		data := doc.Data()
		if data["uid"] != nil {
			http.Error(w, "este DNI ya tiene una cuenta vinculada", http.StatusConflict)
			return
		}

		mailRegistrado, _ := data["email"].(string)

		usuarioAuth, err := authClient.GetUser(ctx, uid)
		if err != nil {
			http.Error(w, "error verificando cuenta", http.StatusInternalServerError)
			return
		}

		if !strings.EqualFold(strings.TrimSpace(usuarioAuth.Email), strings.TrimSpace(mailRegistrado)) {
			http.Error(w, "el mail de tu cuenta no coincide con el registrado para este DNI", http.StatusForbidden)
			return
		}

		_, err = fsClient.Collection("socios").Doc(input.DNI).Update(ctx, []firestore.Update{
			{Path: "uid", Value: uid},
		})
		if err != nil {
			http.Error(w, "error vinculando cuenta", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func VerificarVinculacion(fsClient *firestore.Client, authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		ctx := context.Background()
		iter := fsClient.Collection("socios").Where("uid", "==", uid).Limit(1).Documents(ctx)
		docs, err := iter.GetAll()
		if err != nil {
			http.Error(w, "error consultando", http.StatusInternalServerError)
			return
		}

		vinculado := len(docs) > 0
		json.NewEncoder(w).Encode(map[string]bool{"vinculado": vinculado})
	}
}

func MiSocio(fsClient *firestore.Client, authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		ctx := context.Background()
		iter := fsClient.Collection("socios").Where("uid", "==", uid).Limit(1).Documents(ctx)
		docs, err := iter.GetAll()
		if err != nil || len(docs) == 0 {
			http.Error(w, "socio no encontrado", http.StatusNotFound)
			return
		}

		data := docs[0].Data()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	}
}

func ListarSocios(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()

		// limit: cuántos socios traer por página (default 20)
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
				limit = parsed
			}
		}

		// cursor: el DNI (o ID de doc) del último socio de la página anterior
		cursor := r.URL.Query().Get("cursor")

		query := fsClient.Collection("socios").
			OrderBy(firestore.DocumentID, firestore.Asc).
			Limit(limit)

		if cursor != "" {
			docSnap, err := fsClient.Collection("socios").Doc(cursor).Get(ctx)
			if err != nil {
				http.Error(w, "cursor inválido", http.StatusBadRequest)
				return
			}
			query = query.StartAfter(docSnap)
		}

		docs, err := query.Documents(ctx).GetAll()
		if err != nil {
			http.Error(w, "error obteniendo socios", http.StatusInternalServerError)
			return
		}

		var socios []map[string]interface{}
		for _, doc := range docs {
			data := doc.Data()
			socios = append(socios, map[string]interface{}{
				"id":         doc.Ref.ID,
				"uid":        data["uid"],
				"nombre":     data["nombre"],
				"apellido":   data["apellido"],
				"email":      data["email"],
				"dni":        data["dni"],
				"planes":     data["planes"],
				"estado":     data["estado"],
				"adherentes": data["adherentes"],
			})
		}

		// nextCursor: el ID del último doc de esta página, para pedir la siguiente
		var nextCursor string
		hasMore := len(docs) == limit
		if hasMore {
			nextCursor = docs[len(docs)-1].Ref.ID
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

func ActualizarEstadoSocio(fsClient *firestore.Client) http.HandlerFunc {
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
		docActual, err := fsClient.Collection("socios").Doc(input.ID).Get(ctx)
		if err != nil {
			http.Error(w, "error obteniendo socio actual", http.StatusInternalServerError)
			return
		}
		estadoAnterior, _ := docActual.Data()["estado"].(string)

		updates := []firestore.Update{
			{Path: "estado", Value: input.Estado},
		}

		if input.Adherentes != nil {
			updates = append(updates, firestore.Update{Path: "adherentes", Value: input.Adherentes})
		}

		_, err = fsClient.Collection("socios").Doc(input.ID).Update(ctx, updates)
		if err != nil {
			http.Error(w, "error al actualizar en firestore: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Actualizamos el contador local: restamos del estado viejo, sumamos al nuevo
		if estadoAnterior != input.Estado {
			stats, errStats := leerStats()
			if errStats == nil {
				switch estadoAnterior {
				case "activo":
					stats.Activos--
				case "inactivo":
					stats.Inactivos--
				case "suspendido":
					stats.Suspendidos--
				}
				switch input.Estado {
				case "activo":
					stats.Activos++
				case "inactivo":
					stats.Inactivos++
				case "suspendido":
					stats.Suspendidos++
				}
				guardarStats(stats)
			}
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func ActualizarEstadoPlan(fsClient *firestore.Client) http.HandlerFunc {
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

		ref := fsClient.Collection("socios").Doc(socioID)

		doc, err := ref.Get(ctx)
		if err != nil || !doc.Exists() {
			http.Error(w, "socio no encontrado", http.StatusNotFound)
			return
		}

		data := doc.Data()

		planesRaw, ok := data["planes"].([]interface{})
		if !ok {
			http.Error(w, "el socio no tiene planes válidos", http.StatusInternalServerError)
			return
		}

		encontrado := false

		for i, planRaw := range planesRaw {
			plan, ok := planRaw.(map[string]interface{})
			if !ok {
				continue
			}

			nombre, _ := plan["nombre"].(string)

			if nombre == input.Plan {
				plan["estado"] = input.Estado
				planesRaw[i] = plan
				encontrado = true
				break
			}
		}

		if !encontrado {
			http.Error(w, "plan no encontrado", http.StatusNotFound)
			return
		}

		_, err = ref.Update(ctx, []firestore.Update{
			{
				Path:  "planes",
				Value: planesRaw,
			},
		})

		if err != nil {
			http.Error(w, "error actualizando plan: "+err.Error(), http.StatusInternalServerError)
			return
		}

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

func ImportarSociosCSV(fsClient *firestore.Client) http.HandlerFunc {
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
		bulkWriter := fsClient.BulkWriter(ctx)

		procesados, omitidos := 0, 0
		nuevosActivos, nuevosInactivos := 0, 0

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

			var planesData []interface{}
			for _, plan := range planColumnas {
				if get(plan) == "true" {
					planesData = append(planesData, map[string]interface{}{
						"nombre": plan,
						"estado": "activo",
					})
				}
			}

			estado := mapearEstado(get("Estado de Afiliación"))

			socioDoc := map[string]interface{}{
				"dni":       dni,
				"nombre":    nombre,
				"apellido":  apellido,
				"email":     email,
				"telefono":  get("Telefono"),
				"direccion": get("Dirección"),
				"planes":    planesData,
				"estado":    estado,
			}

			ref := fsClient.Collection("socios").Doc(dni)
			if _, err := bulkWriter.Set(ref, socioDoc, firestore.MergeAll); err != nil {
				log.Printf("error encolando socio %s: %v", dni, err)
				omitidos++
				continue
			}

			procesados++
			if estado == "activo" {
				nuevosActivos++
			} else if estado == "inactivo" {
				nuevosInactivos++
			}
		}

		bulkWriter.End()

		// Nota: esto es un approach simplificado — recalcula sobre lo importado,
		// no diferencia altas nuevas de actualizaciones de socios que ya existían
		// con otro estado. Para stats 100% exactos habría que leer el estado previo
		// de cada uno (lo cual reintroduce lecturas). A definir si les alcanza así.

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "ok",
			"procesados": procesados,
			"omitidos":   omitidos,
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
