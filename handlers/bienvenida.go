package handlers

// Bienvenida de nuevos socios.
//
// PG (tabla "socios_conocidos", creada por el administrador de la BD, NO acá)
// es la fuente de verdad para saber quién es "nuevo": un DNI que no aparece
// en la tabla es un socio recién llegado y entra a la cola de bienvenida
// (mail_bienvenida_enviado_at IS NULL).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
)

// dnisConocidos devuelve el set de DNIs que ya existen en socios_conocidos.
func dnisConocidos(ctx context.Context, dnis []string) (map[string]bool, error) {
	res := make(map[string]bool)
	if len(dnis) == 0 {
		return res, nil
	}

	rows, err := PGPool.Query(ctx, "SELECT dni FROM socios_conocidos WHERE dni = ANY($1::text[])", dnis)
	if err != nil {
		log.Printf("ERROR consultando socios_conocidos: %v", err)
		return res, err
	}
	defer rows.Close()

	for rows.Next() {
		var dni string
		if err := rows.Scan(&dni); err != nil {
			continue
		}
		res[dni] = true
	}
	return res, nil
}

// insertConocidos hace INSERT ... ON CONFLICT (dni) DO NOTHING por lote.
func insertConocidos(ctx context.Context, columns []string, rows [][]interface{}) (int, error) {
	if len(rows) == 0 || len(columns) == 0 {
		return 0, nil
	}

	placeholders := make([]string, 0, len(rows))
	for i := range rows {
		parts := make([]string, 0, len(columns))
		for j := range columns {
			parts = append(parts, fmt.Sprintf("$%d", i*len(columns)+j+1))
		}
		placeholders = append(placeholders, "("+strings.Join(parts, ",")+")")
	}

	args := make([]interface{}, 0, len(rows)*len(columns))
	for _, row := range rows {
		args = append(args, row...)
	}

	query := fmt.Sprintf(
		"INSERT INTO socios_conocidos (%s) VALUES %s ON CONFLICT (dni) DO NOTHING",
		strings.Join(columns, ","),
		strings.Join(placeholders, ","),
	)

	tag, err := PGPool.Exec(ctx, query, args...)
	if err != nil {
		log.Printf("ERROR insertando en socios_conocidos: %v", err)
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// BackfillSociosConocidos registra los socios ya existentes en Firestore como
// "ya vistos" (mail_bienvenida_enviado_at = now()) para que no reciban el mail
// de bienvenida con retroactividad. Se corre UNA sola vez al activar la feature.
func BackfillSociosConocidos(fsClient *firestore.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		ctx := context.Background()
		const pageSize = 500

		registrados := 0
		var cursor *firestore.DocumentSnapshot

		for {
			query := fsClient.Collection("socios").
				OrderBy(firestore.DocumentID, firestore.Asc).
				Limit(pageSize)
			if cursor != nil {
				query = query.StartAfter(cursor)
			}

			docs, err := query.Documents(ctx).GetAll()
			if err != nil {
				http.Error(w, "error leyendo socios", http.StatusInternalServerError)
				return
			}
			if len(docs) == 0 {
				break
			}

			rows := make([][]interface{}, 0, len(docs))
			for _, doc := range docs {
				data := doc.Data()
				email, _ := data["email"].(string)
				if email == "" {
					continue
				}
				rows = append(rows, []interface{}{doc.Ref.ID, email})
			}

			if n, err := insertConocidosConEnvio(ctx, rows); err != nil {
				http.Error(w, "error registrando socios en PG", http.StatusInternalServerError)
				return
			} else {
				registrados += n
			}

			cursor = docs[len(docs)-1]
			if len(docs) < pageSize {
				break
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "ok",
			"registrados": registrados,
		})
	}
}

// insertConocidosConEnvio inserta marcando el mail como ya enviado (backfill).
func insertConocidosConEnvio(ctx context.Context, rows [][]interface{}) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	placeholders := make([]string, 0, len(rows))
	for i := range rows {
		placeholders = append(placeholders, fmt.Sprintf("($%d,$%d, now())", i*2+1, i*2+2))
	}

	args := make([]interface{}, 0, len(rows)*2)
	for _, row := range rows {
		args = append(args, row[0], row[1])
	}

	query := "INSERT INTO socios_conocidos (dni, email, mail_bienvenida_enviado_at) VALUES " +
		strings.Join(placeholders, ",") +
		" ON CONFLICT (dni) DO NOTHING"

	tag, err := PGPool.Exec(ctx, query, args...)
	if err != nil {
		log.Printf("ERROR en backfill de socios_conocidos: %v", err)
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// EnviarBienvenidas procesa la cola de bienvenida por lotes (LIMIT 100).
// Fracasos quedan pendientes para la próxima corrida.
func EnviarBienvenidas() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()

		rows, err := PGPool.Query(ctx, `
			SELECT dni, email
			FROM socios_conocidos
			WHERE mail_bienvenida_enviado_at IS NULL AND email <> ''
			LIMIT 100`)
		if err != nil {
			log.Printf("ERROR leyendo pendientes de bienvenida: %v", err)
			http.Error(w, "error leyendo pendientes", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		type pendiente struct {
			dni, email string
		}

		var lista []pendiente
		for rows.Next() {
			var p pendiente
			if err := rows.Scan(&p.dni, &p.email); err != nil {
				continue
			}
			lista = append(lista, p)
		}

		enviados, fallidos := 0, 0
		for _, p := range lista {
			if err := enviarMailBienvenida(p.email); err != nil {
				log.Printf("ERROR mail bienvenida a dni %s: %v", p.dni, err)
				fallidos++
				continue
			}

			if _, err := PGPool.Exec(ctx,
				`UPDATE socios_conocidos SET mail_bienvenida_enviado_at = now() WHERE dni = $1`,
				p.dni); err != nil {
				log.Printf("ERROR marcando mail enviado para dni %s: %v", p.dni, err)
				fallidos++
				continue
			}
			enviados++
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "ok",
			"enviados": enviados,
			"fallidos": fallidos,
		})
	}
}

// enviarMailBienvenida manda el correo de afiliación completa + link de la app.
// No loguea el payload (ver AGENTS.md, tema 5).
func enviarMailBienvenida(to string) error {
	if APP_LINK == "" {
		return fmt.Errorf("APP_LINK no configurada en .env")
	}

	html := fmt.Sprintf(`
		<p>Hola,</p>
		<p>Tu afiliación a Auren está completa.</p>
		<p>Descargá la app y accedé con tu DNI:</p>
		<p><a href="%s">Descargá la app</a></p>
	`, APP_LINK)

	payload := map[string]interface{}{
		"from":    "Auren <admin@formulariosalud.com.ar>",
		"to":      []string{to},
		"subject": "Tu afiliación a Auren está completa",
		"html":    html,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+RESEND_API_KEY)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("resend devolvió status %d", resp.StatusCode)
	}
	return nil
}
