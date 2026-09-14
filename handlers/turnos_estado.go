package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// CompletarTurnosVencidos pasa a 'completado' los turnos asignados cuya
// fecha/hora ya pasó y no fueron cancelados. Así un turno pasado no se puede
// cancelar desde la app (otra cancelación liberaría el cupo mensual) y cuenta
// como ya usado del mes.
func CompletarTurnosVencidos() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tag, err := PGPool.Exec(ctx, `
		UPDATE turnos
		SET estado = 'completado', completado_en = NOW()
		WHERE estado = 'asignado'
		  AND fecha <> ''
		  AND hora <> ''
		  AND to_timestamp(fecha || ' ' || hora, 'YYYY-MM-DD HH24:MI') < NOW()`)
	if err != nil {
		log.Printf("ERROR marcando turnos como completado: %v", err)
		return
	}

	if tag.RowsAffected() > 0 {
		log.Printf("TURNOS COMPLETADOS por vencimiento: %d", tag.RowsAffected())
	}
}

// IniciarCompletadoAutomatico corre completarTurnosVencidos cada minuto en
// background, más una pasada inmediata al arrancar.
func IniciarCompletadoAutomatico() {
	go func() {
		CompletarTurnosVencidos()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			CompletarTurnosVencidos()
		}
	}()
}

// CancelarTurnoAdmin cancela un turno desde el panel (RequireAdmin).
// Solo estados 'pendiente' o 'asignado'; un 'completado' ya no se puede
// cancelar (liberaría el cupo del mes).
func CancelarTurnoAdmin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input struct {
			TurnoID string `json:"turnoId"`
			Motivo  string `json:"motivo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input.TurnoID = strings.TrimSpace(input.TurnoID)
		input.Motivo = strings.TrimSpace(input.Motivo)
		if input.TurnoID == "" {
			http.Error(w, "falta turnoId", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var estadoActual string
		err := PGPool.QueryRow(ctx,
			`SELECT estado FROM turnos WHERE id::text = $1`,
			input.TurnoID,
		).Scan(&estadoActual)
		if err != nil {
			http.Error(w, "turno no encontrado", http.StatusNotFound)
			return
		}

		if estadoActual != "pendiente" && estadoActual != "asignado" {
			http.Error(w, fmt.Sprintf("no se puede cancelar un turno en estado %q desde el panel", estadoActual), http.StatusConflict)
			return
		}

		_, err = PGPool.Exec(ctx, `
			UPDATE turnos SET
				estado = 'cancelado',
				motivo_cancelacion = $2,
				cancelado_por = 'admin',
				cancelado_en = NOW()
			WHERE id::text = $1`,
			input.TurnoID, input.Motivo,
		)
		if err != nil {
			log.Printf("ERROR cancelando turno %s (admin): %v", input.TurnoID, err)
			http.Error(w, "error al cancelar el turno", http.StatusInternalServerError)
			return
		}

		log.Printf("TURNO CANCELADO por el admin id=%s motivo=%q", input.TurnoID, input.Motivo)

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}