package handlers

import (
	"context"
	"log"
)

// ReconstruirStats recorre TODA la tabla de socios una sola vez y reescribe
// estadisticas.json con los números reales.
// Se llama a mano cuando hace falta (primera vez, o si el archivo
// se corrompe/desincroniza) — nunca queda expuesta como endpoint HTTP.
func ReconstruirStats() error {
	ctx := context.Background()

	rows, err := PGPool.Query(ctx, `
		SELECT estado, COALESCE(jsonb_array_length(adherentes), 0)
		FROM socios`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var stats Estadisticas
	for rows.Next() {
		var estado string
		var totalAdherentes int
		if err := rows.Scan(&estado, &totalAdherentes); err != nil {
			continue
		}
		stats.TotalSocios++
		stats.TotalAdherentes += totalAdherentes

		switch estado {
		case "activo":
			stats.Activos++
		case "inactivo":
			stats.Inactivos++
		case "suspendido":
			stats.Suspendidos++
		}
	}

	if err := guardarStats(stats); err != nil {
		return err
	}

	log.Printf("✅ estadisticas.json reconstruido: %+v", stats)
	return nil
}