package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
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

type MedicoRow struct {
	DNI          string
	Nombre       string
	Apellido     string
	Especialidad string
	Ciudad       string
	Provincia    string
	Direccion    string
	Imagen       string
	Lat          float64
	Lng          float64
}

func CrearMedico() http.HandlerFunc {
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

		// Geocodificamos la dirección para poder calcular cercanía después.
		// Si falla, no bloqueamos el alta — el médico queda con lat/lng 0 y
		// simplemente no aparece ordenado por distancia.
		direccionCompleta := fmt.Sprintf("%s, %s, %s", input.Direccion, input.Ciudad, input.Provincia)
		lat, lng, errGeo := geocodificarDireccion(direccionCompleta)
		if errGeo != nil {
			log.Printf("WARNING: no se pudo geocodificar médico %s: %v", input.DNI, errGeo)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		tag, err := PGPool.Exec(ctx,
			`INSERT INTO medicos (dni, nombre, apellido, especialidad, ciudad, provincia, direccion, imagen, lat, lng)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 ON CONFLICT (dni) DO NOTHING`,
			input.DNI, input.Nombre, input.Apellido, input.Especialidad,
			input.Ciudad, input.Provincia, input.Direccion, input.Imagen, lat, lng,
		)
		if err != nil {
			log.Printf("ERROR insertando médico %s: %v", input.DNI, err)
			http.Error(w, "error guardando medico", http.StatusInternalServerError)
			return
		}

		if tag.RowsAffected() == 0 {
			http.Error(w, "el médico ya existe", http.StatusConflict)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func ListarMedicos() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := verifyIDToken(r, AuthClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		rows, err := PGPool.Query(ctx,
			`SELECT dni, nombre, apellido, especialidad, ciudad, provincia, direccion, imagen
			 FROM medicos ORDER BY apellido, nombre`)
		if err != nil {
			http.Error(w, "error obteniendo medicos", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		medicos := make([]map[string]interface{}, 0)
		for rows.Next() {
			var m MedicoRow
			if err := rows.Scan(&m.DNI, &m.Nombre, &m.Apellido, &m.Especialidad,
				&m.Ciudad, &m.Provincia, &m.Direccion, &m.Imagen); err != nil {
				continue
			}
			medicos = append(medicos, map[string]interface{}{
				"id":           m.DNI,
				"nombre":       m.Nombre,
				"apellido":     m.Apellido,
				"dni":          m.DNI,
				"especialidad": m.Especialidad,
				"provincia":    m.Provincia,
				"ciudad":       m.Ciudad,
				"direccion":    m.Direccion,
				"imagen":       m.Imagen,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(medicos)
	}
}

// ---------- Estructura compartida para sugerencias ----------

type SugerenciaLugar struct {
	Tipo         string  `json:"tipo"`
	ID           string  `json:"id"`
	Nombre       string  `json:"nombre"`
	Apellido     string  `json:"apellido"`
	Especialidad string  `json:"especialidad"`
	Direccion    string  `json:"direccion"`
	Ciudad       string  `json:"ciudad"`
	DistanciaKm  float64 `json:"distanciaKm"`
}

// ---------- Sugerir médicos + clínicas cercanas (admin) ----------

func SugerirMedicosCercanos() http.HandlerFunc {
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

		turnoCtx, cancelTurno := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelTurno()

		var especialidad, direccionTurno, ciudadTurno string
		var latTurno, lngTurno float64
		err := PGPool.QueryRow(turnoCtx, `
			SELECT COALESCE(especialidad,''), COALESCE(lat,0), COALESCE(lng,0),
				   COALESCE(direccion,''), COALESCE(ciudad,'')
			FROM turnos WHERE id::text = $1`, turnoID).
			Scan(&especialidad, &latTurno, &lngTurno, &direccionTurno, &ciudadTurno)
		if err != nil {
			http.Error(w, "turno no encontrado", http.StatusNotFound)
			return
		}

		if latTurno == 0 && lngTurno == 0 {
			if direccionTurno == "" {
				http.Error(w, "el turno no tiene dirección cargada", http.StatusBadRequest)
				return
			}
			direccionCompleta := fmt.Sprintf("%s, %s", direccionTurno, ciudadTurno)
			latTurno, lngTurno, err = geocodificarDireccion(direccionCompleta)
			if err != nil {
				log.Printf("WARNING: no se pudo geocodificar turno %s: %v", turnoID, err)
				http.Error(w, "no se pudo ubicar la dirección del turno", http.StatusUnprocessableEntity)
				return
			}
		}

		espNormalizada := normalizarEspecialidad(especialidad)

		queryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var resultado []SugerenciaLugar

		// ── 1. MÉDICOS ──
		medRows, err := PGPool.Query(queryCtx,
			`SELECT dni, nombre, apellido, especialidad, direccion, ciudad, provincia, lat, lng
			 FROM medicos`)
		if err != nil {
			log.Printf("ERROR obteniendo médicos para sugerencia: %v", err)
		} else {
			defer medRows.Close()
			for medRows.Next() {
				var m MedicoRow
				if err := medRows.Scan(&m.DNI, &m.Nombre, &m.Apellido, &m.Especialidad,
					&m.Direccion, &m.Ciudad, &m.Provincia, &m.Lat, &m.Lng); err != nil {
					continue
				}

				if espNormalizada != "" && normalizarEspecialidad(m.Especialidad) != espNormalizada {
					continue
				}

				if m.Lat == 0 && m.Lng == 0 {
					partes := []string{m.Direccion, m.Ciudad, m.Provincia}
					filtrados := make([]string, 0, len(partes))
					for _, p := range partes {
						if strings.TrimSpace(p) != "" {
							filtrados = append(filtrados, strings.TrimSpace(p))
						}
					}
					direccionCompleta := strings.Join(filtrados, ", ")
					m.Lat, m.Lng, err = geocodificarDireccion(direccionCompleta)
					if err != nil {
						log.Printf("WARNING: no se pudo geocodificar médico %s: %v", m.DNI, err)
						continue
					}
					_, _ = PGPool.Exec(queryCtx,
						`UPDATE medicos SET lat = $1, lng = $2 WHERE dni = $3`,
						m.Lat, m.Lng, m.DNI)
				}

				resultado = append(resultado, SugerenciaLugar{
					Tipo:         "medico",
					ID:           m.DNI,
					Nombre:       m.Nombre,
					Apellido:     m.Apellido,
					Especialidad: m.Especialidad,
					Direccion:    m.Direccion,
					Ciudad:       m.Ciudad,
					DistanciaKm:  distanciaKm(latTurno, lngTurno, m.Lat, m.Lng),
				})
			}
		}

		// ── 2. CLÍNICAS ──
		clinRows, err := PGPool.Query(queryCtx,
			`SELECT c.id, c.nombre, c.direccion, c.ciudad, c.provincia, c.lat, c.lng
			 FROM clinicas c`)
		if err != nil {
			log.Printf("WARNING: tabla clinicas no existe o error: %v", err)
		} else {
			defer clinRows.Close()
			for clinRows.Next() {
				var c ClinicaRow
				if err := clinRows.Scan(&c.ID, &c.Nombre, &c.Direccion, &c.Ciudad, &c.Provincia, &c.Lat, &c.Lng); err != nil {
					continue
				}

				// Verificar que la clínica tenga la especialidad pedida.
				espClinicaRows, err := PGPool.Query(queryCtx,
					`SELECT especialidad FROM clinica_especialidades WHERE clinica_id = $1`, c.ID)
				if err != nil {
					continue
				}
				tieneEspecialidad := false
				var espMatch string
				for espClinicaRows.Next() {
					var esp string
					if err := espClinicaRows.Scan(&esp); err != nil {
						continue
					}
					if normalizarEspecialidad(esp) == espNormalizada {
						tieneEspecialidad = true
						espMatch = esp
						break
					}
				}
				espClinicaRows.Close()
				if !tieneEspecialidad {
					continue
				}

				if c.Lat == 0 && c.Lng == 0 {
					partes := []string{c.Direccion, c.Ciudad, c.Provincia}
					filtrados := make([]string, 0, len(partes))
					for _, p := range partes {
						if strings.TrimSpace(p) != "" {
							filtrados = append(filtrados, strings.TrimSpace(p))
						}
					}
					direccionCompleta := strings.Join(filtrados, ", ")
					c.Lat, c.Lng, err = geocodificarDireccion(direccionCompleta)
					if err != nil {
						log.Printf("WARNING: no se pudo geocodificar clínica %d: %v", c.ID, err)
						continue
					}
					_, _ = PGPool.Exec(queryCtx,
						`UPDATE clinicas SET lat = $1, lng = $2 WHERE id = $3`,
						c.Lat, c.Lng, c.ID)
				}

				resultado = append(resultado, SugerenciaLugar{
					Tipo:         "clinica",
					ID:           fmt.Sprintf("%d", c.ID),
					Nombre:       c.Nombre,
					Apellido:     "",
					Especialidad: espMatch,
					Direccion:    c.Direccion,
					Ciudad:       c.Ciudad,
					DistanciaKm:  distanciaKm(latTurno, lngTurno, c.Lat, c.Lng),
				})
			}
		}

		sort.Slice(resultado, func(i, j int) bool {
			return resultado[i].DistanciaKm < resultado[j].DistanciaKm
		})

		json.NewEncoder(w).Encode(resultado)
	}
}

// BorrarMedico elimina un médico por DNI (solo admin).
func BorrarMedico() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input struct {
			DNI string `json:"dni"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.DNI == "" {
			http.Error(w, "dni requerido", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		tag, err := PGPool.Exec(ctx,
			`DELETE FROM medicos WHERE dni = $1`, input.DNI)
		if err != nil {
			http.Error(w, "error al borrar: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			http.Error(w, "médico no encontrado", http.StatusNotFound)
			return
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

// normalizarEspecialidad deja la especialidad en minúsculas, sin tildes y sin
// espacios de más, para poder comparar "Clínica Médica" (médico) contra
// "Clinica Medica" (turno creado desde la app) sin que el usuario vea que "no
// se encontró la especialidad".
func normalizarEspecialidad(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "á", "a")
	s = strings.ReplaceAll(s, "é", "e")
	s = strings.ReplaceAll(s, "í", "i")
	s = strings.ReplaceAll(s, "ó", "o")
	s = strings.ReplaceAll(s, "ú", "u")
	s = strings.ReplaceAll(s, "ü", "u")
	s = strings.ReplaceAll(s, "ñ", "n")
	return strings.Join(strings.Fields(s), " ")
}