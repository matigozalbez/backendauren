package handlers

// handlers/socios.go

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	 "regexp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/auth"
 	"google.golang.org/api/iterator"
	"strconv"
	"log"
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

if err == nil && doc.Exists() {
	http.Error(w, "el socio ya existe", http.StatusConflict)
	return
}

if err != nil && status.Code(err) != codes.NotFound {
	http.Error(w, "error verificando socio", http.StatusInternalServerError)
	return
}

		
		
		_, err = fsClient.Collection("socios").Doc(input.DNI).Set(ctx, map[string]interface{}{
			"dni":                   input.DNI,
			"nombre":                input.Nombre,
			"apellido":              input.Apellido,
			"email":                 input.Email,
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
		iter := fsClient.Collection("socios").Documents(ctx)
		docs, err := iter.GetAll()
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

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(socios)
	}
}

func ActualizarEstadoSocio(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		// Estructura específica para este endpoint
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

		// Preparamos los campos a actualizar en Firestore
		updates := []firestore.Update{
			{Path: "estado", Value: input.Estado},
		}

		// Si mandaste adherentes actualizados, los incluimos también
		if input.Adherentes != nil {
			updates = append(updates, firestore.Update{Path: "adherentes", Value: input.Adherentes})
		}

		_, err := fsClient.Collection("socios").Doc(input.ID).Update(ctx, updates)
		if err != nil {
			http.Error(w, "error al actualizar en firestore: "+err.Error(), http.StatusInternalServerError)
			return
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
func ListarHistorial(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		ctx := context.Background()

		q := fsClient.Collection("historial_turnos").Query

		if estado := r.URL.Query().Get("estado"); estado != "" {
			q = q.Where("estado", "==", estado)
		}

		if socioDni := r.URL.Query().Get("socioDni"); socioDni != "" {
			q = q.Where("socioDni", "==", socioDni)
		}

		q = q.OrderBy("asignadoEn", firestore.Desc)

		iter := q.Documents(ctx)
		defer iter.Stop()

		historial := make([]HistorialAdminView, 0)
		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Printf("ERROR LEYENDO HISTORIAL: %v", err)
				http.Error(w, "error leyendo historial: "+err.Error(), http.StatusInternalServerError)
				return
			}

			var h HistorialAdminView
			data := doc.Data()
			b, _ := json.Marshal(data)
			_ = json.Unmarshal(b, &h)
			h.ID = doc.Ref.ID
			historial = append(historial, h)
		}

		json.NewEncoder(w).Encode(historial)
	}
}