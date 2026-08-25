package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/messaging"
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

		turnos := make([]TurnoAdminView, 0)
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

func AsignarMedico(
	fsClient *firestore.Client,
	msgClient *messaging.Client,
) http.HandlerFunc {
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

		// =========================================================
		// 1. BUSCAR TURNO
		// =========================================================

		turnoRef := fsClient.Collection("turnos").Doc(input.TurnoID)

		turnoSnap, err := turnoRef.Get(ctx)
		if err != nil {
			http.Error(w, "turno no encontrado", http.StatusNotFound)
			return
		}

		turnoData := turnoSnap.Data()

		if estadoActual, _ := turnoData["estado"].(string); estadoActual == "cancelado" {
			http.Error(
				w,
				"no se puede asignar un turno cancelado",
				http.StatusConflict,
			)
			return
		}

		// Datos del usuario que solicitó el turno
		uid, _ := turnoData["uid"].(string)
		socioEmail, _ := turnoData["socioEmail"].(string)
		beneficiarioNombre, _ := turnoData["beneficiarioNombre"].(string)
		especialidad, _ := turnoData["especialidad"].(string)
		ciudad, _ := turnoData["ciudad"].(string)

		log.Printf(
			"ASIGNANDO TURNO id=%s uid=%s email=%s beneficiario=%s",
			input.TurnoID,
			uid,
			socioEmail,
			beneficiarioNombre,
		)

		// =========================================================
		// 2. BUSCAR MÉDICO
		// =========================================================

		medicoSnap, err := fsClient.
			Collection("medicos").
			Doc(input.MedicoID).
			Get(ctx)

		if err != nil {
			http.Error(w, "médico no encontrado", http.StatusNotFound)
			return
		}

		medicoData := medicoSnap.Data()

		medicoNombre, _ := medicoData["nombre"].(string)
		medicoApellido, _ := medicoData["apellido"].(string)
		medicoDireccion, _ := medicoData["direccion"].(string)

		// =========================================================
		// 3. ACTUALIZAR TURNO
		// =========================================================

		updates := []firestore.Update{
			{Path: "estado", Value: "asignado"},
			{Path: "medicoId", Value: input.MedicoID},
			{Path: "medicoNombre", Value: medicoNombre},
			{Path: "medicoApellido", Value: medicoApellido},
			{Path: "medicoDireccion", Value: medicoDireccion},
			{Path: "asignadoEn", Value: firestore.ServerTimestamp},
		}

		if input.Fecha != "" {
			updates = append(
				updates,
				firestore.Update{
					Path:  "fecha",
					Value: input.Fecha,
				},
			)
		}

		if input.Hora != "" {
			updates = append(
				updates,
				firestore.Update{
					Path:  "hora",
					Value: input.Hora,
				},
			)
		}

		if _, err := turnoRef.Update(ctx, updates); err != nil {
			log.Printf(
				"ERROR actualizando turno %s: %v",
				input.TurnoID,
				err,
			)

			http.Error(
				w,
				"error al asignar médico",
				http.StatusInternalServerError,
			)
			return
		}

		log.Printf(
			"TURNO ASIGNADO correctamente: %s",
			input.TurnoID,
		)

		// =========================================================
		// 4. PUSH NOTIFICATION
		// =========================================================

		if uid != "" && msgClient != nil {

			tokenSnap, err := fsClient.
				Collection("push_tokens").
				Doc(uid).
				Get(ctx)

			if err != nil {
				log.Printf(
					"WARNING: no se encontró push token para uid=%s: %v",
					uid,
					err,
				)
			} else {

				tokenData := tokenSnap.Data()

				token, _ := tokenData["token"].(string)

				if token != "" {

					push := &messaging.Message{
						Token: token,

						Webpush: &messaging.WebpushConfig{
							Notification: &messaging.WebpushNotification{
								Title: "Turno asignado ✅",
								Body: fmt.Sprintf(
									"Tu turno de %s fue asignado para el %s a las %s.",
									especialidad,
									input.Fecha,
									input.Hora,
								),
								Icon: "/icon-192.png",
							},
						},
					}

					_, err := msgClient.Send(ctx, push)

					if err != nil {
						log.Printf(
							"ERROR enviando push al uid=%s: %v",
							uid,
							err,
						)
					} else {
						log.Printf(
							"PUSH enviado correctamente al uid=%s",
							uid,
						)
					}
				} else {
					log.Printf(
						"WARNING: push_tokens/%s no tiene token",
						uid,
					)
				}
			}
		}

		// =========================================================
		// 5. EMAIL CON RESEND
		// =========================================================

		if socioEmail != "" {

			err := enviarEmailTurnoAsignado(
				socioEmail,
				beneficiarioNombre,
				especialidad,
				medicoNombre,
				medicoApellido,
				medicoDireccion,
				ciudad,
				input.Fecha,
				input.Hora,
			)

			if err != nil {
				log.Printf(
					"ERROR enviando email de turno a %s: %v",
					socioEmail,
					err,
				)
			} else {
				log.Printf(
					"EMAIL de turno enviado correctamente a %s",
					socioEmail,
				)
			}

		} else {
			log.Printf(
				"WARNING: turno %s no tiene socioEmail",
				input.TurnoID,
			)
		}

		// =========================================================
		// 6. RESPUESTA
		// =========================================================

		w.WriteHeader(http.StatusOK)

		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	}
}


func enviarEmailTurnoAsignado(
	destinatario string,
	nombre string,
	especialidad string,
	medicoNombre string,
	medicoApellido string,
	medicoDireccion string,
	ciudad string,
	fecha string,
	hora string,
) error {

	log.Printf(
		"DEBUG: enviando email de turno asignado a=%q",
		destinatario,
	)

	html := fmt.Sprintf(`
		<div style="font-family: Arial, sans-serif; max-width: 600px; margin: auto;">

			<h2 style="color:#0F1E3D;">
				Tu turno fue asignado ✅
			</h2>

			<p>
				Hola %s,
			</p>

			<p>
				Tu solicitud de turno fue asignada correctamente.
			</p>

			<div style="
				background:#F8F5EF;
				padding:20px;
				border-radius:16px;
				margin:20px 0;
			">

				<p>
					<strong>Especialidad:</strong><br>
					%s
				</p>

				<p>
					<strong>Profesional:</strong><br>
					Dr. %s %s
				</p>

				<p>
					<strong>Fecha:</strong><br>
					%s
				</p>

				<p>
					<strong>Hora:</strong><br>
					%s
				</p>

				<p>
					<strong>Dirección:</strong><br>
					%s<br>
					%s
				</p>

			</div>

			<p>
				Podés consultar los detalles de tu turno
				desde Auren.
			</p>

		</div>
	`,
		nombre,
		especialidad,
		medicoNombre,
		medicoApellido,
		fecha,
		hora,
		medicoDireccion,
		ciudad,
	)

	payload := map[string]interface{}{
		"from": "Auren <admin@formulariosalud.com.ar>",
		"to": []string{
			destinatario,
		},
		"subject": "Tu turno fue asignado - Auren",
		"html":    html,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	log.Printf(
		"DEBUG: payload Resend preparado para %s",
		destinatario,
	)

	req, err := http.NewRequest(
		"POST",
		"https://api.resend.com/emails",
		bytes.NewBuffer(jsonData),
	)

	if err != nil {
		return err
	}

	req.Header.Set(
		"Authorization",
		"Bearer "+RESEND_API_KEY,
	)

	req.Header.Set(
		"Content-Type",
		"application/json",
	)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	log.Printf(
		"DEBUG: Resend respondió status=%d body=%s",
		resp.StatusCode,
		string(body),
	)

	if resp.StatusCode >= 300 {
		return fmt.Errorf(
			"resend devolvió status %d: %s",
			resp.StatusCode,
			string(body),
		)
	}

	return nil
}