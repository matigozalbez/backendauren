package handlers

import (
	"context"
	"log"

	"cloud.google.com/go/firestore"
)

// ReconstruirStats recorre TODA la colección de socios una sola vez
// y reescribe estadisticas.json con los números reales.
// Se llama a mano cuando hace falta (primera vez, o si el archivo
// se corrompe/desincroniza) — nunca queda expuesta como endpoint HTTP.
func ReconstruirStats(fsClient *firestore.Client) error {
	ctx := context.Background()

	iter := fsClient.Collection("socios").Documents(ctx)
	docs, err := iter.GetAll()
	if err != nil {
		return err
	}

	var stats Estadisticas
	for _, doc := range docs {
		data := doc.Data()
		stats.TotalSocios++

		estado, _ := data["estado"].(string)
		switch estado {
		case "activo":
			stats.Activos++
		case "inactivo":
			stats.Inactivos++
		case "suspendido":
			stats.Suspendidos++
		}

		if adherentes, ok := data["adherentes"].([]interface{}); ok {
			stats.TotalAdherentes += len(adherentes)
		}
	}

	if err := guardarStats(stats); err != nil {
		return err
	}

	log.Printf("✅ estadisticas.json reconstruido: %+v", stats)
	return nil
}
