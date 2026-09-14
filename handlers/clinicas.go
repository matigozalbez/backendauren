package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/messaging"
)

type ClinicaInput struct {
	Nombre         string   `json:"nombre"`
	Ciudad         string   `json:"ciudad"`
	Provincia      string   `json:"provincia"`
	Especialidades []string `json:"especialidades"`
	Direccion      string   `json:"direccion"`
	Imagen         string   `json:"imagen"`
}

type ClinicaRow struct {
	ID       int
	Nombre   string
	Ciudad   string
	Provincia string
	Direccion string
	Imagen   string
	Lat      float64
	Lng      float64
}

func CrearClinica() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input ClinicaInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if input.Nombre == "" || input.Ciudad == "" || input.Direccion == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}

		especialidades := make([]string, 0, len(input.Especialidades))
		vistos := map[string]bool{}
		for _, esp := range input.Especialidades {
			esp = strings.TrimSpace(esp)
			if esp == "" {
				continue
			}
			if vistos[esp] {
				continue
			}
			vistos[esp] = true
			especialidades = append(especialidades, esp)
		}

		if len(especialidades) == 0 {
			http.Error(w, "falta al menos una especialidad", http.StatusBadRequest)
			return
		}

		direccionCompleta := fmt.Sprintf("%s, %s, %s", input.Direccion, input.Ciudad, input.Provincia)
		lat, lng, errGeo := geocodificarDireccion(direccionCompleta)
		if errGeo != nil {
			log.Printf("WARNING: no se pudo geocodificar clínica %s: %v", input.Nombre, errGeo)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		tx, err := PGPool.Begin(ctx)
		if err != nil {
			http.Error(w, "error iniciando transacción", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback(ctx)

		var clinicaID int
		err = tx.QueryRow(ctx,
			`INSERT INTO clinicas (nombre, direccion, ciudad, provincia, imagen, lat, lng)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 RETURNING id`,
			input.Nombre, input.Direccion, input.Ciudad, input.Provincia, input.Imagen, lat, lng,
		).Scan(&clinicaID)
		if err != nil {
			log.Printf("ERROR insertando clínica %s: %v", input.Nombre, err)
			http.Error(w, "error guardando clinica", http.StatusInternalServerError)
			return
		}

		for _, esp := range especialidades {
			_, err = tx.Exec(ctx,
				`INSERT INTO clinica_especialidades (clinica_id, especialidad)
				 VALUES ($1, $2)`,
				clinicaID, esp,
			)
			if err != nil {
				log.Printf("ERROR insertando especialidad %q de clínica %d: %v", esp, clinicaID, err)
				http.Error(w, "error guardando especialidades", http.StatusInternalServerError)
				return
			}
		}

		if err := tx.Commit(ctx); err != nil {
			http.Error(w, "error confirmando clínica", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func ListarClinicas() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := verifyIDToken(r, AuthClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		rows, err := PGPool.Query(ctx,
			`SELECT id, nombre, direccion, ciudad, provincia, imagen
			 FROM clinicas ORDER BY nombre`)
		if err != nil {
			http.Error(w, "error obteniendo clinicas", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		clinicas := make([]map[string]interface{}, 0)
		ids := make([]int, 0)
		for rows.Next() {
			var c ClinicaRow
			if err := rows.Scan(&c.ID, &c.Nombre, &c.Direccion, &c.Ciudad, &c.Provincia, &c.Imagen); err != nil {
				continue
			}
			clinicas = append(clinicas, map[string]interface{}{
				"id":         c.ID,
				"nombre":     c.Nombre,
				"direccion":  c.Direccion,
				"ciudad":     c.Ciudad,
				"provincia":  c.Provincia,
				"imagen":     c.Imagen,
				"especialidades": []string{},
			})
			ids = append(ids, c.ID)
		}

		if len(ids) > 0 {
			espRows, err := PGPool.Query(ctx,
				`SELECT clinica_id, especialidad FROM clinica_especialidades ORDER BY clinica_id, especialidad`)
			if err == nil {
				defer espRows.Close()
				porID := map[int]int{}
				for i, c := range clinicas {
					porID[c["id"].(int)] = i
				}
				for espRows.Next() {
					var climicaID int
					var esp string
					if err := espRows.Scan(&climicaID, &esp); err != nil {
						continue
					}
					if idx, ok := porID[climicaID]; ok {
						lista := clinicas[idx]["especialidades"].([]string)
						lista = append(lista, esp)
						clinicas[idx]["especialidades"] = lista
					}
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(clinicas)
	}
}

func EditarClinica() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input struct {
			ID             int      `json:"id"`
			ClinicaInput
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		if input.ID == 0 || input.Nombre == "" || input.Ciudad == "" || input.Direccion == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}

		especialidades := make([]string, 0, len(input.Especialidades))
		vistos := map[string]bool{}
		for _, esp := range input.Especialidades {
			esp = strings.TrimSpace(esp)
			if esp == "" || vistos[esp] {
				continue
			}
			vistos[esp] = true
			especialidades = append(especialidades, esp)
		}

		if len(especialidades) == 0 {
			http.Error(w, "falta al menos una especialidad", http.StatusBadRequest)
			return
		}

		var currentLat, currentLng float64
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := PGPool.QueryRow(ctx,
			`SELECT lat, lng FROM clinicas WHERE id = $1`, input.ID,
		).Scan(&currentLat, &currentLng)
		if err != nil {
			http.Error(w, "clínica no encontrada", http.StatusNotFound)
			return
		}

		direccionCompleta := fmt.Sprintf("%s, %s, %s", input.Direccion, input.Ciudad, input.Provincia)
		lat, lng, errGeo := geocodificarDireccion(direccionCompleta)
		if errGeo != nil {
			log.Printf("WARNING: no se pudo geocodificar clínica %d: %v", input.ID, errGeo)
			lat, lng = currentLat, currentLng
		}

		tx, err := PGPool.Begin(ctx)
		if err != nil {
			http.Error(w, "error iniciando transacción", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback(ctx)

		tag, err := tx.Exec(ctx,
			`UPDATE clinicas SET nombre = $1, direccion = $2, ciudad = $3, provincia = $4, imagen = $5, lat = $6, lng = $7
			 WHERE id = $8`,
			input.Nombre, input.Direccion, input.Ciudad, input.Provincia, input.Imagen, lat, lng, input.ID,
		)
		if err != nil {
			log.Printf("ERROR actualizando clínica %d: %v", input.ID, err)
			http.Error(w, "error actualizando clinica", http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			http.Error(w, "clínica no encontrada", http.StatusNotFound)
			return
		}

		_, err = tx.Exec(ctx, `DELETE FROM clinica_especialidades WHERE clinica_id = $1`, input.ID)
		if err != nil {
			http.Error(w, "error borrando especialidades", http.StatusInternalServerError)
			return
		}

		for _, esp := range especialidades {
			_, err = tx.Exec(ctx,
				`INSERT INTO clinica_especialidades (clinica_id, especialidad) VALUES ($1, $2)`,
				input.ID, esp,
			)
			if err != nil {
				http.Error(w, "error guardando especialidades", http.StatusInternalServerError)
				return
			}
		}

		if err := tx.Commit(ctx); err != nil {
			http.Error(w, "error confirmando clínica", http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func BorrarClinica() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input struct {
			ID int `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.ID == 0 {
			http.Error(w, "id requerido", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		tag, err := PGPool.Exec(ctx, `DELETE FROM clinicas WHERE id = $1`, input.ID)
		if err != nil {
			http.Error(w, "error al borrar: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			http.Error(w, "clínica no encontrada", http.StatusNotFound)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

// ---------- Asignar clínica a un turno (admin) ----------

type AsignarClinicaInput struct {
	TurnoID  string `json:"turnoId"`
	ClinicaID int   `json:"clinicaId"`
	Fecha    string `json:"fecha"`
	Hora     string `json:"hora"`
}

func AsignarClinica(
	fsClient *firestore.Client,
	msgClient *messaging.Client,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input AsignarClinicaInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.TurnoID = strings.TrimSpace(input.TurnoID)

		if input.TurnoID == "" || input.ClinicaID == 0 {
			http.Error(w, "faltan turnoId o clinicaId", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		turnoCtx, cancelTurno := context.WithTimeout(ctx, 5*time.Second)
		defer cancelTurno()

		var uid, socioEmail, beneficiarioNombre, especialidad, ciudad, estadoActual, tipoTurno string
		var fechaActual, horaActual string
		err := PGPool.QueryRow(turnoCtx, `
			SELECT uid, COALESCE(socio_email,''), COALESCE(beneficiario_nombre,''),
				   COALESCE(especialidad,''), COALESCE(ciudad,''), estado,
				   COALESCE(fecha,''), COALESCE(hora,''), COALESCE(tipo,'consulta')
			FROM turnos WHERE id::text = $1`,
			input.TurnoID,
		).Scan(&uid, &socioEmail, &beneficiarioNombre, &especialidad, &ciudad,
			&estadoActual, &fechaActual, &horaActual, &tipoTurno)
		if err != nil {
			http.Error(w, "turno no encontrado", http.StatusNotFound)
			return
		}

		esEstudio := tipoTurno == "estudio"

		if estadoActual == "cancelado" {
			http.Error(w, "no se puede asignar un turno cancelado", http.StatusConflict)
			return
		}

		log.Printf(
			"ASIGNANDO TURNO A CLÍNICA id=%s uid=%s email=%s beneficiario=%s",
			input.TurnoID, uid, socioEmail, beneficiarioNombre,
		)

		// Buscar clínica y verificar que tenga la especialidad del turno.
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		var clinicaNombre, clinicaDireccion, clinicaCiudad, clinicaProvincia string
		err = PGPool.QueryRow(queryCtx,
			`SELECT nombre, direccion, ciudad, provincia FROM clinicas WHERE id = $1`,
			input.ClinicaID,
		).Scan(&clinicaNombre, &clinicaDireccion, &clinicaCiudad, &clinicaProvincia)
		if err != nil {
			http.Error(w, "clínica no encontrada", http.StatusNotFound)
			return
		}

		matchea := esEstudio
		if !esEstudio {
			espRows, err := PGPool.Query(queryCtx,
				`SELECT especialidad FROM clinica_especialidades WHERE clinica_id = $1`,
				input.ClinicaID,
			)
			if err != nil {
				http.Error(w, "error obteniendo especialidades de la clínica", http.StatusInternalServerError)
				return
			}
			defer espRows.Close()

			for espRows.Next() {
				var esp string
				if err := espRows.Scan(&esp); err != nil {
					continue
				}
				if normalizarEspecialidad(esp) == normalizarEspecialidad(especialidad) {
					matchea = true
					break
				}
			}
		}
		if !matchea {
			http.Error(w, "la clínica no atiende esa especialidad", http.StatusUnprocessableEntity)
			return
		}

		// Dirección COMPLETA (calle, ciudad y provincia) para "cómo llegar".
		partesDir := []string{clinicaDireccion, clinicaCiudad, clinicaProvincia}
		partesFiltradas := make([]string, 0, len(partesDir))
		for _, p := range partesDir {
			if strings.TrimSpace(p) != "" {
				partesFiltradas = append(partesFiltradas, strings.TrimSpace(p))
			}
		}
		clinicaDireccion = strings.Join(partesFiltradas, ", ")

		fechaFinal := fechaActual
		if input.Fecha != "" {
			fechaFinal = input.Fecha
		}
		horaFinal := horaActual
		if input.Hora != "" {
			horaFinal = input.Hora
		}

		updCtx, cancelUpd := context.WithTimeout(ctx, 5*time.Second)
		defer cancelUpd()

		_, err = PGPool.Exec(updCtx, `
			UPDATE turnos SET
				estado = 'asignado',
				clinica_id = $2,
				clinica_nombre = $3,
				clinica_direccion = $4,
				fecha = $5,
				hora = $6,
				asignado_en = NOW()
			WHERE id::text = $1`,
			input.TurnoID,
			fmt.Sprintf("%d", input.ClinicaID),
			clinicaNombre,
			clinicaDireccion,
			fechaFinal,
			horaFinal,
		)
		if err != nil {
			log.Printf("ERROR actualizando turno %s (clínica): %v", input.TurnoID, err)
			http.Error(w, "error al asignar clínica", http.StatusInternalServerError)
			return
		}

		// Al asignar a una clínica, limpiamos el médico del turno (si hubo uno).
		_, _ = PGPool.Exec(updCtx, `
			UPDATE turnos SET
				medico_id = NULL,
				medico_nombre = NULL,
				medico_apellido = NULL,
				medico_direccion = NULL
			WHERE id::text = $1`,
			input.TurnoID,
		)

		// Historial (PG).
		histCtx, cancelHist := context.WithTimeout(ctx, 5*time.Second)
		defer cancelHist()

		_, err = PGPool.Exec(histCtx, `
			INSERT INTO historial_turnos (
				turno_id, uid, especialidad, medico_nombre, medico_apellido, fecha, hora, clinica_nombre
			) VALUES ((SELECT id FROM turnos WHERE id::text = $1), $2, $3, $4, $5, $6, $7, $8)`,
			input.TurnoID,
			uid,
			especialidad,
			"",
			"",
			fechaFinal,
			horaFinal,
			clinicaNombre,
		)
		if err != nil {
			log.Printf("WARNING: error guardando historial del turno %s (clínica): %v", input.TurnoID, err)
		}

		// Push.
		if uid != "" && msgClient != nil {
			tokenSnap, err := fsClient.Collection("push_tokens").Doc(uid).Get(ctx)
			if err != nil {
				log.Printf("WARNING: no se encontró push token para uid=%s: %v", uid, err)
			} else {
				tokenData := tokenSnap.Data()
				token, _ := tokenData["token"].(string)
				if token != "" {
					palabra := "turno"
					if esEstudio {
						palabra = "estudio"
					}
					push := &messaging.Message{
						Token: token,
						Webpush: &messaging.WebpushConfig{
							Notification: &messaging.WebpushNotification{
								Title: "Turno asignado ✅",
								Body: fmt.Sprintf(
									"Tu %s de %s fue asignado en %s para el %s a las %s.",
									palabra, especialidad, clinicaNombre, fechaFinal, horaFinal,
								),
								Icon: "/icon-192.png",
							},
						},
					}
					_, err := msgClient.Send(ctx, push)
					if err != nil {
						log.Printf("ERROR enviando push al uid=%s: %v", uid, err)
					}
				}
			}
		}

		// Email.
		if socioEmail != "" {
			err := enviarEmailTurnoAsignado(
				esEstudio,
				socioEmail,
				beneficiarioNombre,
				especialidad,
				"",
				clinicaNombre,
				"",
				clinicaDireccion,
				fechaFinal,
				horaFinal,
			)
			if err != nil {
				log.Printf("ERROR enviando email de turno a %s: %v", socioEmail, err)
			}
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}