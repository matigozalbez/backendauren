package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"firebase.google.com/go/v4/auth"
)

func MisTurnos(
	authClient *auth.Client,
) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		w.Header().Set("Content-Type", "application/json")

		log.Printf("MIS TURNOS: request recibida")

		if r.Method != http.MethodGet {

			log.Printf(
				"MIS TURNOS: método no permitido: %s",
				r.Method,
			)

			http.Error(
				w,
				"método no permitido",
				http.StatusMethodNotAllowed,
			)

			return
		}

		uid, err := verifyIDToken(r, authClient)

		if err != nil {

			log.Printf(
				"MIS TURNOS: error verificando token: %v",
				err,
			)

			http.Error(
				w,
				"no autorizado, iniciá sesión",
				http.StatusUnauthorized,
			)

			return
		}

		log.Printf(
			"MIS TURNOS: UID autenticado: %s",
			uid,
		)

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := PGPool.Query(ctx, `
			SELECT id::text, uid, socio_dni, solicitado_por,
				   es_para_adherente, beneficiario_dni, beneficiario_nombre,
				   COALESCE(tipo,'consulta'), especialidad, ciudad, direccion, motivo, estado, modo,
				   COALESCE(imagen_url,''), COALESCE(franja_preferida,''),
				   COALESCE(medico_id,''), COALESCE(medico_nombre,''), COALESCE(medico_apellido,''),
				   COALESCE(medico_direccion,''), COALESCE(fecha,''), COALESCE(hora,''),
				   COALESCE(clinica_id,''), COALESCE(clinica_nombre,''), COALESCE(clinica_direccion,'')
			FROM turnos WHERE uid = $1
			ORDER BY creado_en DESC`,
			uid,
		)
		if err != nil {
			log.Printf("MIS TURNOS: ERROR PG: %v", err)
			http.Error(w, "error leyendo tus turnos: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		turnos := make([]TurnoAdminView, 0)

		for rows.Next() {

			var turno TurnoAdminView

			if err := rows.Scan(&turno.ID, &turno.Uid, &turno.SocioDni, &turno.SolicitadoPor,
				&turno.EsParaAdherente, &turno.BeneficiarioDni, &turno.BeneficiarioNombre,
				&turno.Tipo, &turno.Especialidad, &turno.Ciudad, &turno.Direccion, &turno.Motivo, &turno.Estado, &turno.Modo,
				&turno.ImagenURL, &turno.FranjaPreferida,
				&turno.MedicoID, &turno.MedicoNombre, &turno.MedicoApellido, &turno.MedicoDireccion,
				&turno.Fecha, &turno.Hora,
				&turno.ClinicaID, &turno.ClinicaNombre, &turno.ClinicaDireccion); err != nil {
				log.Printf("MIS TURNOS: error scan turno: %v", err)
				continue
			}

			log.Printf(
				"MIS TURNOS: turno encontrado: %s",
				turno.ID,
			)

			turnos = append(turnos, turno)
		}

		log.Printf(
			"MIS TURNOS: encontrados %d turnos para uid=%s",
			len(turnos),
			uid,
		)

		if err := json.NewEncoder(w).Encode(turnos); err != nil {

			log.Printf(
				"MIS TURNOS: error enviando respuesta: %v",
				err,
			)

			return
		}
	}
}