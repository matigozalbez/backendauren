package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

const nombrePlanSalud = "Auren Salud"

type AccesoAurenSalud struct {
	SocioEstado     string
	PlanSaludEstado string
	SocioActivo     bool
	PlanSaludActivo bool
}

// evaluarAccesoAurenSalud determina si el socio tiene derecho a usar los
// servicios exclusivos de Auren Salud (turnos y estudios médicos).
// El socio debe estar activo y tener el plan "Auren Salud" en estado activo.
func evaluarAccesoAurenSalud(data map[string]interface{}) *AccesoAurenSalud {
	acceso := &AccesoAurenSalud{SocioActivo: true, PlanSaludActivo: false}

	estado, _ := data["estado"].(string)
	if estado == "" {
		estado = "activo"
	}
	acceso.SocioEstado = estado
	acceso.SocioActivo = estado == "activo"

	planesRaw, ok := data["planes"].([]interface{})
	if !ok {
		return acceso
	}

	for _, raw := range planesRaw {
		plan, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}

		nombre, _ := plan["nombre"].(string)
		if !strings.EqualFold(strings.TrimSpace(nombre), nombrePlanSalud) {
			continue
		}

		planEstado, _ := plan["estado"].(string)
		if planEstado == "" {
			planEstado = "activo"
		}
		acceso.PlanSaludEstado = planEstado
		acceso.PlanSaludActivo = planEstado == "activo"
		break
	}

	return acceso
}

// responderSinAccesoSalud responde 403 con código y mensaje claros para la app.
func responderSinAccesoSalud(w http.ResponseWriter, acceso *AccesoAurenSalud) {
	codigo := "PLAN_SALUD_INACTIVO"
	mensaje := "Su plan auren salud se encuentra inactivo"

	if !acceso.SocioActivo {
		codigo = "SOCIO_INACTIVO"
		mensaje = "Usted no se encuentra activo para usar los servicios de auren salud"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "error",
		"codigo":  codigo,
		"mensaje": mensaje,
	})
}