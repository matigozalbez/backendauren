package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Estadisticas es lo que consume el Dashboard del panel. Los números se
// calculan al vuelo desde la tabla socios: no hay archivo que mantener ni
// contadores incrementales que se puedan desincronizar.
type Estadisticas struct {
	TotalSocios     int `json:"totalSocios"`
	Activos         int `json:"activos"`
	Inactivos       int `json:"inactivos"`
	Suspendidos     int `json:"suspendidos"`
	TotalAdherentes int `json:"totalAdherentes"`
}

// EstadisticasSocios cuenta los socios directo de Postgres. La tabla socios es
// la única fuente de verdad: cualquier alta o cambio de estado se refleja acá
// sin importar por qué camino entró.
func EstadisticasSocios(w http.ResponseWriter, r *http.Request) {
	if PGPool == nil {
		http.Error(w, "base de datos no disponible", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var stats Estadisticas
	err := PGPool.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE estado = 'activo'),
			count(*) FILTER (WHERE estado = 'inactivo'),
			count(*) FILTER (WHERE estado = 'suspendido'),
			COALESCE(sum(
				CASE WHEN jsonb_typeof(adherentes) = 'array'
				     THEN jsonb_array_length(adherentes)
				     ELSE 0 END
			), 0)
		FROM socios`).Scan(
		&stats.TotalSocios,
		&stats.Activos,
		&stats.Inactivos,
		&stats.Suspendidos,
		&stats.TotalAdherentes,
	)
	if err != nil {
		http.Error(w, "error calculando estadísticas", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
