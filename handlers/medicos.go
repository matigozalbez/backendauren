package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/auth"
)

type MedicoInput struct {
	Nombre       string `json:"nombre"`
	Apellido     string `json:"apellido"`
	DNI          string `json:"dni"`
	Ciudad       string `json:"ciudad"`
	Provincia    string `json:"provincia"`
	Especialidad string `json:"especialidad"`
	Direccion    string `json:"direccion"`
	Imagen       string `json:"imagen"`
}

func CrearMedico(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input MedicoInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if input.Nombre == "" || input.Apellido == "" || input.DNI == "" ||
			input.Ciudad == "" || input.Especialidad == "" || input.Direccion == "" {
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

		ctx := context.Background()

		doc, err := fsClient.Collection("medicos").Doc(input.DNI).Get(ctx)

		if err == nil && doc.Exists() {
			http.Error(w, "el médico ya existe", http.StatusConflict)
			return
		}

		if err != nil && status.Code(err) != codes.NotFound {
			http.Error(w, "error verificando médico", http.StatusInternalServerError)
			return
		}

		// Geocodificamos la dirección para poder calcular cercanía después.
		// Si falla, no bloqueamos el alta — el médico queda sin lat/lng y
		// simplemente no aparece ordenado por distancia hasta que se corrija.
		direccionCompleta := fmt.Sprintf("%s, %s, %s", input.Direccion, input.Ciudad, input.Provincia)
		lat, lng, errGeo := geocodificarDireccion(direccionCompleta)
		if errGeo != nil {
			log.Printf("WARNING: no se pudo geocodificar médico %s: %v", input.DNI, errGeo)
		}

		_, err = fsClient.Collection("medicos").Doc(input.DNI).Set(ctx, map[string]interface{}{
			"nombre":       input.Nombre,
			"apellido":     input.Apellido,
			"dni":          input.DNI,
			"ciudad":       input.Ciudad,
			"provincia":    input.Provincia,
			"especialidad": input.Especialidad,
			"direccion":    input.Direccion,
			"imagen":       input.Imagen,
			"lat":          lat,
			"lng":          lng,
			"uid":          nil,
		})
		if err != nil {
			http.Error(w, "error guardando medico", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func ListarMedicos(fsClient *firestore.Client, authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		_, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		ctx := context.Background()

		iter := fsClient.Collection("medicos").Documents(ctx)
		docs, err := iter.GetAll()
		if err != nil {
			http.Error(w, "error obteniendo medicos", http.StatusInternalServerError)
			return
		}

		medicos := make([]map[string]interface{}, 0)
		for _, doc := range docs {
			data := doc.Data()
			medicos = append(medicos, map[string]interface{}{
				"id":           doc.Ref.ID,
				"nombre":       data["nombre"],
				"apellido":     data["apellido"],
				"dni":          data["dni"],
				"especialidad": data["especialidad"],
				"provincia":    data["provincia"],
				"ciudad":       data["ciudad"],
				"direccion":    data["direccion"],
				"imagen":       data["imagen"],
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(medicos)
	}
}

type MedicoConDistancia struct {
	ID           string  `json:"id"`
	Nombre       string  `json:"nombre"`
	Apellido     string  `json:"apellido"`
	Especialidad string  `json:"especialidad"`
	Direccion    string  `json:"direccion"`
	Ciudad       string  `json:"ciudad"`
	DistanciaKm  float64 `json:"distanciaKm"`
}

func SugerirMedicosCercanos(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		turnoID := strings.TrimSpace(r.URL.Query().Get("turnoId"))
		if turnoID == "" {
			http.Error(w, "falta turnoId", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		turnoSnap, err := fsClient.Collection("turnos").Doc(turnoID).Get(ctx)
		if err != nil {
			http.Error(w, "turno no encontrado", http.StatusNotFound)
			return
		}

		turnoData := turnoSnap.Data()
		especialidad, _ := turnoData["especialidad"].(string)
		direccionTurno, _ := turnoData["direccion"].(string)
		ciudadTurno, _ := turnoData["ciudad"].(string)

		if direccionTurno == "" {
			http.Error(w, "el turno no tiene dirección cargada", http.StatusBadRequest)
			return
		}

		// Geocodificamos la dirección del socio/turno en el momento.
		// (Si más adelante guardan lat/lng del socio de antemano, acá se
		// podría reusar en vez de pegarle a la API de nuevo cada vez.)
		direccionCompleta := fmt.Sprintf("%s, %s", direccionTurno, ciudadTurno)
		latTurno, lngTurno, err := geocodificarDireccion(direccionCompleta)
		if err != nil {
			log.Printf("WARNING: no se pudo geocodificar turno %s: %v", turnoID, err)
			http.Error(w, "no se pudo ubicar la dirección del turno", http.StatusUnprocessableEntity)
			return
		}

		// Traemos los médicos de esa especialidad
		query := fsClient.Collection("medicos")
		var docs []*firestore.DocumentSnapshot
		if especialidad != "" {
			docs, err = query.Where("especialidad", "==", especialidad).Documents(ctx).GetAll()
		} else {
			docs, err = query.Documents(ctx).GetAll()
		}
		if err != nil {
			http.Error(w, "error obteniendo médicos", http.StatusInternalServerError)
			return
		}

		var resultado []MedicoConDistancia
		for _, doc := range docs {
			data := doc.Data()

			lat, okLat := data["lat"].(float64)
			lng, okLng := data["lng"].(float64)
			if !okLat || !okLng || (lat == 0 && lng == 0) {
				continue // médico sin geocodificar todavía, lo excluimos del ranking
			}

			nombre, _ := data["nombre"].(string)
			apellido, _ := data["apellido"].(string)
			direccion, _ := data["direccion"].(string)
			ciudad, _ := data["ciudad"].(string)

			resultado = append(resultado, MedicoConDistancia{
				ID:           doc.Ref.ID,
				Nombre:       nombre,
				Apellido:     apellido,
				Especialidad: especialidad,
				Direccion:    direccion,
				Ciudad:       ciudad,
				DistanciaKm:  distanciaKm(latTurno, lngTurno, lat, lng),
			})
		}

		sort.Slice(resultado, func(i, j int) bool {
			return resultado[i].DistanciaKm < resultado[j].DistanciaKm
		})

		json.NewEncoder(w).Encode(resultado)
	}
}
