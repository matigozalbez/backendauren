package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"firebase.google.com/go/v4/messaging"
	"github.com/jackc/pgx/v5"
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
	Tipo               string `json:"tipo"`
	Especialidad       string `json:"especialidad"`
	Ciudad             string `json:"ciudad"`
	Direccion          string `json:"direccion"`
	Motivo             string `json:"motivo"`
	Estado             string `json:"estado"`
	Modo               string `json:"modo"`
	ImagenURL          string `json:"imagenUrl,omitempty"`
	FranjaPreferida    string `json:"franjaPreferida,omitempty"`
	MotivoCancelacion  string `json:"motivoCancelacion,omitempty"`
	CanceladoEn        string `json:"canceladoEn,omitempty"`
	MedicoID           string `json:"medicoId,omitempty"`
	MedicoNombre       string `json:"medicoNombre,omitempty"`
	MedicoApellido     string `json:"medicoApellido,omitempty"`
	MedicoDireccion    string `json:"medicoDireccion,omitempty"`
	ClinicaID          string `json:"clinicaId,omitempty"`
	ClinicaNombre      string `json:"clinicaNombre,omitempty"`
	ClinicaDireccion   string `json:"clinicaDireccion,omitempty"`
	Fecha              string `json:"fecha,omitempty"`
	Hora               string `json:"hora,omitempty"`
}

// requireAdmin ya se aplica como middleware en main.go, así que acá no se vuelve a chequear.
func ListarTurnos() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		query := `SELECT id::text, uid, socio_dni, solicitado_por,
			es_para_adherente, beneficiario_dni, beneficiario_nombre,
			COALESCE(tipo,'consulta'), especialidad, ciudad, direccion, motivo, estado, modo,
			COALESCE(imagen_url,''), COALESCE(franja_preferida,''),
			COALESCE(motivo_cancelacion,''), COALESCE(cancelado_en::text,''),
			COALESCE(medico_id,''), COALESCE(medico_nombre,''), COALESCE(medico_apellido,''),
			COALESCE(medico_direccion,''), COALESCE(fecha,''), COALESCE(hora,''),
			COALESCE(clinica_id,''), COALESCE(clinica_nombre,''), COALESCE(clinica_direccion,'')
			FROM turnos`

		if estado := r.URL.Query().Get("estado"); estado != "" {
			query += ` WHERE estado = '` + strings.ReplaceAll(estado, "'", "''") + `' ORDER BY creado_en DESC`
		} else {
			query += ` ORDER BY creado_en DESC`
		}

		rows, err := PGPool.Query(ctx, query)
		if err != nil {
			log.Printf("ERROR LEYENDO TURNOS: %v", err)
			http.Error(w, "error leyendo turnos: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		turnos := make([]TurnoAdminView, 0)
		for rows.Next() {
			var t TurnoAdminView
			if err := rows.Scan(&t.ID, &t.Uid, &t.SocioDni, &t.SolicitadoPor,
				&t.EsParaAdherente, &t.BeneficiarioDni, &t.BeneficiarioNombre,
				&t.Tipo, &t.Especialidad, &t.Ciudad, &t.Direccion, &t.Motivo, &t.Estado, &t.Modo,
				&t.ImagenURL, &t.FranjaPreferida, &t.MotivoCancelacion, &t.CanceladoEn,
				&t.MedicoID, &t.MedicoNombre, &t.MedicoApellido, &t.MedicoDireccion,
				&t.Fecha, &t.Hora,
				&t.ClinicaID, &t.ClinicaNombre, &t.ClinicaDireccion); err != nil {
				continue
			}
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
		// 1. BUSCAR TURNO (ahora en PostgreSQL)
		// =========================================================

		turnoCtx, cancelTurno := context.WithTimeout(ctx, 5*time.Second)
		defer cancelTurno()

		var uid, socioEmail, beneficiarioNombre, especialidad, ciudad, estadoActual string
		var fechaActual, horaActual string
		err := PGPool.QueryRow(turnoCtx, `
			SELECT uid, COALESCE(socio_email,''), COALESCE(beneficiario_nombre,''),
				   COALESCE(especialidad,''), COALESCE(ciudad,''), estado,
				   COALESCE(fecha,''), COALESCE(hora,'')
			FROM turnos WHERE id::text = $1`,
			input.TurnoID,
		).Scan(&uid, &socioEmail, &beneficiarioNombre, &especialidad, &ciudad,
			&estadoActual, &fechaActual, &horaActual)
		if err != nil {
			http.Error(w, "turno no encontrado", http.StatusNotFound)
			return
		}

		if estadoActual == "cancelado" {
			http.Error(
				w,
				"no se puede asignar un turno cancelado",
				http.StatusConflict,
			)
			return
		}

		log.Printf(
			"ASIGNANDO TURNO id=%s uid=%s email=%s beneficiario=%s",
			input.TurnoID,
			uid,
			socioEmail,
			beneficiarioNombre,
		)

		// =========================================================
		// 2. BUSCAR MÉDICO (desde PostgreSQL)
		// =========================================================

		var medicoNombre, medicoApellido, medicoDireccion, medicoCiudad, medicoProvincia string
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		err = PGPool.QueryRow(queryCtx,
			`SELECT COALESCE(nombre,''), COALESCE(apellido,''), COALESCE(direccion,''),
			        COALESCE(ciudad,''), COALESCE(provincia,'')
			 FROM medicos WHERE dni = $1`,
			input.MedicoID,
		).Scan(&medicoNombre, &medicoApellido, &medicoDireccion, &medicoCiudad, &medicoProvincia)

		if err != nil {
			// Un 404 real es que no existe; cualquier otro error (columna NULL,
			// tipo, red) antes devolvía el mismo 404 y escondía el problema real.
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "médico no encontrado", http.StatusNotFound)
				return
			}
			log.Printf("ERROR obteniendo médico %s: %v", input.MedicoID, err)
			http.Error(w, "error obteniendo médico", http.StatusInternalServerError)
			return
		}

		// Guardamos la dirección COMPLETA del médico (calle, ciudad y provincia).
		// La app usa este campo para "cómo llegar": si solo guardás la calle,
		// Google Maps busca la dirección en la ciudad del turno (que es la del
		// socio) y nunca encuentra al consultorio (ej. médico en San José del
		// Rincón y turno creado en Santa Fe).
		partesDir := []string{medicoDireccion, medicoCiudad, medicoProvincia}
		partesFiltradas := make([]string, 0, len(partesDir))
		for _, p := range partesDir {
			if strings.TrimSpace(p) != "" {
				partesFiltradas = append(partesFiltradas, strings.TrimSpace(p))
			}
		}
		medicoDireccion = strings.Join(partesFiltradas, ", ")

		// =========================================================
		// 3. ACTUALIZAR TURNO (PostgreSQL)
		// =========================================================

		updCtx, cancelUpd := context.WithTimeout(ctx, 5*time.Second)
		defer cancelUpd()

		fechaFinal := fechaActual
		if input.Fecha != "" {
			fechaFinal = input.Fecha
		}
		horaFinal := horaActual
		if input.Hora != "" {
			horaFinal = input.Hora
		}

		_, err = PGPool.Exec(updCtx, `
			UPDATE turnos SET
				estado = 'asignado',
				medico_id = $2,
				medico_nombre = $3,
				medico_apellido = $4,
				medico_direccion = $5,
				fecha = $6,
				hora = $7,
				asignado_en = NOW()
			WHERE id::text = $1`,
			input.TurnoID,
			input.MedicoID,
			medicoNombre,
			medicoApellido,
			medicoDireccion,
			fechaFinal,
			horaFinal,
		)
		if err != nil {
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

		// Guardamos el historial en PostgreSQL (no se escribe más en Firestore).
		histCtx, cancelHist := context.WithTimeout(ctx, 5*time.Second)
		defer cancelHist()

		_, err = PGPool.Exec(histCtx, `
			INSERT INTO historial_turnos (
				turno_id, uid, especialidad, medico_nombre, medico_apellido, fecha, hora
			) VALUES ((SELECT id FROM turnos WHERE id::text = $1), $2, $3, $4, $5, $6, $7)`,
			input.TurnoID,
			uid,
			especialidad,
			medicoNombre,
			medicoApellido,
			fechaFinal,
			horaFinal,
		)
		if err != nil {
			// No cortamos el flujo: el turno ya quedó asignado correctamente,
			// esto es solo el registro de historial para el admin.
			log.Printf(
				"WARNING: error guardando historial del turno %s: %v",
				input.TurnoID,
				err,
			)
		}

		
		if uid != "" && msgClient != nil {

			var token string
			err := PGPool.QueryRow(ctx,
				`SELECT token FROM push_tokens WHERE uid = $1`, uid,
			).Scan(&token)

			if err != nil {
				log.Printf(
					"WARNING: no se encontró push token para uid=%s: %v",
					uid,
					err,
				)
			} else if token != "" {

					push := &messaging.Message{
						Token: token,

						Webpush: &messaging.WebpushConfig{
							Notification: &messaging.WebpushNotification{
								Title: "Turno asignado ✅",
								Body: fmt.Sprintf(
									"Tu turno de %s fue asignado para el %s a las %s.",
									especialidad,
									fechaFinal,
									horaFinal,
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

		// =========================================================
		// 5. EMAIL CON RESEND
		// =========================================================

		if socioEmail != "" {

			err := enviarEmailTurnoAsignado(
				false,
				socioEmail,
				beneficiarioNombre,
				especialidad,
				"Dr.",
				medicoNombre,
				medicoApellido,
				medicoDireccion,
				fechaFinal,
				horaFinal,
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
	esEstudio bool,
	destinatario string,
	nombre string,
	especialidad string,
	profesionalTitulo string,
	profesionalNombre string,
	profesionalApellido string,
	profesionalDireccion string,
	fecha string,
	hora string,
) error {

	log.Printf(
		"DEBUG: enviando email de turno asignado a=%q",
		destinatario,
	)

	palabra := "turno"
	if esEstudio {
		palabra = "estudio"
	}

	profesional := strings.TrimSpace(profesionalTitulo + " " + profesionalNombre + " " + profesionalApellido)

	html := fmt.Sprintf(`
		<div style="font-family: Arial, sans-serif; max-width: 600px; margin: auto;">

			<h2 style="color:#0F1E3D;">
				Tu %s fue asignado ✅
			</h2>

			<p>
				Hola %s,
			</p>

			<p>
				Tu solicitud de %s fue asignada correctamente.
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
					%s
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
					%s
				</p>

			</div>

			<p>
				Podés consultar los detalles de tu %s
				desde Auren.
			</p>

		</div>
	`,
		strings.ToUpper(palabra),
		nombre,
		palabra,
		especialidad,
		profesional,
		fecha,
		hora,
		profesionalDireccion,
		palabra,
	)

	payload := map[string]interface{}{
		"from": "Auren <admin@formulariosalud.com.ar>",
		"to": []string{
			destinatario,
		},
		"subject": fmt.Sprintf("Tu %s fue asignado - Auren", palabra),
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