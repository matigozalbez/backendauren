package handlers

import (
    "context"
    "encoding/json"
    "log"
    "net/http"
    "strings"

    "cloud.google.com/go/firestore"
    "google.golang.org/api/iterator"
)

// ---------- Listar turnos (admin) ----------

type TurnoAdminView struct {
	ID                 string `json:"id"`
	Uid                string `json:"uid"`
	SocioDni           string `json:"socioDni"`
	SolicitadoPor      string `json:"solicitadoPor"`
	EsParaAdherente    bool   `json:"esParaAdherente"`
	BeneficiarioDni    string `json:"beneficiarioDni"`
	BeneficiarioNombre string `json:"beneficiarioNombre"`
	Especialidad       string `json:"especialidad"`
	Ciudad             string `json:"ciudad"`
	Direccion          string `json:"direccion"`
	Motivo             string `json:"motivo"`
	Estado             string `json:"estado"`
	MedicoID           string `json:"medicoId,omitempty"`
	MedicoNombre       string `json:"medicoNombre,omitempty"`
	MedicoApellido     string `json:"medicoApellido,omitempty"`
	MedicoDireccion    string `json:"medicoDireccion,omitempty"`
	Fecha              string `json:"fecha,omitempty"`
	Hora               string `json:"hora,omitempty"`
}

// requireAdmin ya se aplica como middleware en main.go, así que acá no se vuelve a chequear.
func ListarTurnos(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		ctx := context.Background()

query := fsClient.Collection("turnos").OrderBy("creadoEn", firestore.Desc)

if estado := r.URL.Query().Get("estado"); estado != "" {
	query = fsClient.Collection("turnos").
		Where("estado", "==", estado).
		OrderBy("creadoEn", firestore.Desc)
}

		iter := query.Documents(ctx)
		defer iter.Stop()

		var turnos []TurnoAdminView
		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
		if err != nil {
    log.Printf("ERROR LEYENDO TURNOS: %v", err)

    http.Error(
        w,
        "error leyendo turnos: "+err.Error(),
        http.StatusInternalServerError,
    )

    return
}

			var t TurnoAdminView
			data := doc.Data()
			b, _ := json.Marshal(data)
			_ = json.Unmarshal(b, &t)
			t.ID = doc.Ref.ID
			turnos = append(turnos, t)
		}

		json.NewEncoder(w).Encode(turnos)
	}
}

// ---------- Asignar médico a un turno (admin) ----------

type AsignarMedicoInput struct {
	TurnoID  string `json:"turnoId"`
	MedicoID string `json:"medicoId"`
	Fecha    string `json:"fecha"` // opcional, ej "2026-09-02"
	Hora     string `json:"hora"`  // opcional, ej "10:30"
}

func AsignarMedico(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input AsignarMedicoInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.TurnoID = strings.TrimSpace(input.TurnoID)
		input.MedicoID = strings.TrimSpace(input.MedicoID)

		if input.TurnoID == "" || input.MedicoID == "" {
			http.Error(w, "faltan turnoId o medicoId", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		turnoRef := fsClient.Collection("turnos").Doc(input.TurnoID)
		turnoSnap, err := turnoRef.Get(ctx)
		if err != nil {
			http.Error(w, "turno no encontrado", http.StatusNotFound)
			return
		}
		if estadoActual, _ := turnoSnap.Data()["estado"].(string); estadoActual == "cancelado" {
			http.Error(w, "no se puede asignar un turno cancelado", http.StatusConflict)
			return
		}

		medicoSnap, err := fsClient.Collection("medicos").Doc(input.MedicoID).Get(ctx)
		if err != nil {
			http.Error(w, "médico no encontrado", http.StatusNotFound)
			return
		}
		medicoData := medicoSnap.Data()
		medicoNombre, _ := medicoData["nombre"].(string)
		medicoApellido, _ := medicoData["apellido"].(string)
		medicoDireccion, _ := medicoData["direccion"].(string)

		updates := []firestore.Update{
			{Path: "estado", Value: "asignado"},
			{Path: "medicoId", Value: input.MedicoID},
			{Path: "medicoNombre", Value: medicoNombre},
			{Path: "medicoApellido", Value: medicoApellido},
			{Path: "medicoDireccion", Value: medicoDireccion},
			{Path: "asignadoEn", Value: firestore.ServerTimestamp},
		}

		if input.Fecha != "" {
			updates = append(updates, firestore.Update{Path: "fecha", Value: input.Fecha})
		}
		if input.Hora != "" {
			updates = append(updates, firestore.Update{Path: "hora", Value: input.Hora})
		}

		if _, err := turnoRef.Update(ctx, updates); err != nil {
			http.Error(w, "error al asignar médico", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}