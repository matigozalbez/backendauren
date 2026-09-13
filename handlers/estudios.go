package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"firebase.google.com/go/v4/auth"
)

const maxImagenEstudioBytes = 5 * 1024 * 1024 // 5 MB

// Estados permitidos para un estudio en el panel admin.
var estadosEstudioValidos = map[string]bool{
	"pendiente":   true,
	"en_proceso":  true,
	"aprobado":    true,
	"rechazado":   true,
	"cancelado":   true,
}

// SubirImagenEstudio sube una foto tomada por el socio a Cloudinary y devuelve
// la URL pública. Solo el usuario autenticado (verifyIDToken) puede subir.
func SubirImagenEstudio(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		if CLOUDINARY_CLOUD_NAME == "" || CLOUDINARY_API_KEY == "" || CLOUDINARY_API_SECRET == "" {
			log.Println("ERROR: credenciales Cloudinary ausentes")
			http.Error(w, "subida de imágenes no configurada", http.StatusInternalServerError)
			return
		}

		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado, iniciá sesión", http.StatusUnauthorized)
			return
		}

		if err := r.ParseMultipartForm(maxImagenEstudioBytes + (1 << 20)); err != nil {
			http.Error(w, "imagen demasiado grande o multipart inválido", http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "falta el archivo de imagen (campo 'file')", http.StatusBadRequest)
			return
		}
		defer file.Close()

		if header.Size > maxImagenEstudioBytes {
			http.Error(w, "la imagen supera los 5 MB", http.StatusRequestEntityTooLarge)
			return
		}

		contentType := header.Header.Get("Content-Type")
		if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/webp" {
			http.Error(w, "solo se admiten imágenes PNG, JPG o WebP", http.StatusBadRequest)
			return
		}

		contenido, err := io.ReadAll(file)
		if err != nil || len(contenido) == 0 {
			http.Error(w, "error leyendo la imagen", http.StatusBadRequest)
			return
		}

		url, err := SubirACloudinary(uid, contentType, contenido)
		if err != nil {
			log.Printf("ERROR subiendo imagen a Cloudinary para uid=%s: %v", uid, err)
			http.Error(w, "error subiendo la imagen, intentalo de nuevo", http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"url":    url,
		})
	}
}

// ListarEstudios devuelve los turnos tipo 'estudio' con toda la info para el
// panel admin (socio, estudio, ciudad, estado, clínica, imagen). RequireAdmin.
func ListarEstudios() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		query := `SELECT id::text, uid, socio_dni, solicitado_por,
			es_para_adherente, beneficiario_dni, beneficiario_nombre,
			COALESCE(tipo,'consulta'), especialidad, ciudad, direccion, motivo, estado, modo,
			COALESCE(imagen_url,''), COALESCE(franja_preferida,''),
			COALESCE(medico_id,''), COALESCE(medico_nombre,''), COALESCE(medico_apellido,''),
			COALESCE(medico_direccion,''), COALESCE(fecha,''), COALESCE(hora,''),
			COALESCE(clinica_id,''), COALESCE(clinica_nombre,''), COALESCE(clinica_direccion,'')
			FROM turnos WHERE tipo = 'estudio'`

		if estado := r.URL.Query().Get("estado"); estado != "" {
			query += ` AND estado = '` + strings.ReplaceAll(estado, "'", "''") + `'`
		}
		query += ` ORDER BY creado_en DESC`

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := PGPool.Query(ctx, query)
		if err != nil {
			log.Printf("ERROR listando estudios: %v", err)
			http.Error(w, "error leyendo estudios: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		estudios := make([]TurnoAdminView, 0)
		for rows.Next() {
			var t TurnoAdminView
			if err := rows.Scan(&t.ID, &t.Uid, &t.SocioDni, &t.SolicitadoPor,
				&t.EsParaAdherente, &t.BeneficiarioDni, &t.BeneficiarioNombre,
				&t.Tipo, &t.Especialidad, &t.Ciudad, &t.Direccion, &t.Motivo, &t.Estado, &t.Modo,
				&t.ImagenURL, &t.FranjaPreferida,
				&t.MedicoID, &t.MedicoNombre, &t.MedicoApellido, &t.MedicoDireccion,
				&t.Fecha, &t.Hora,
				&t.ClinicaID, &t.ClinicaNombre, &t.ClinicaDireccion); err != nil {
				continue
			}
			estudios = append(estudios, t)
		}

		json.NewEncoder(w).Encode(estudios)
	}
}

// CambiarEstadoEstudio actualiza el estado de un estudio desde el panel admin.
// RequireAdmin. Estados: pendiente, en_proceso, aprobado, rechazado, cancelado.
func CambiarEstadoEstudio() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input struct {
			TurnoID string `json:"turnoId"`
			Estado  string `json:"estado"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.TurnoID = strings.TrimSpace(input.TurnoID)
		input.Estado = strings.TrimSpace(input.Estado)
		if !estadosEstudioValidos[input.Estado] {
			http.Error(w, "estado inválido", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		tag, err := PGPool.Exec(ctx,
			`UPDATE turnos SET estado = $1 WHERE id::text = $2 AND tipo = 'estudio'`,
			input.Estado, input.TurnoID,
		)
		if err != nil {
			log.Printf("ERROR actualizando estado del estudio %s: %v", input.TurnoID, err)
			http.Error(w, "error actualizando el estado", http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			http.Error(w, "estudio no encontrado", http.StatusNotFound)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}