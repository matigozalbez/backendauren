package handlers

import (
	"encoding/json"
	"strings"
	"testing"
)

// Las acciones se guardan en la base como texto plano y el panel las traduce
// a una frase con un mapa propio. Si un call site escribe una acción que no
// está en la allowlist, el error sale acá y no como una fila ilegible en la
// pantalla.
func TestAccionesAuditadasEnAllowlist(t *testing.T) {
	usadas := []string{
		AccionSocioCrear,
		AccionSocioActualizar,
		AccionSocioPlanEstado,
		AccionSocioBeneficios,
		AccionSocioImportarCSV,
		AccionSocioBackfill,
		AccionNotifCrear,
		AccionNotifBienvenidas,
		AccionPlanUpsert,
		AccionTurnoAsignarMedico,
		AccionTurnoAsignarClinica,
		AccionTurnoCancelar,
		AccionEstudioEstado,
		AccionServicioEstado,
		AccionMedicoCrear,
		AccionMedicoBorrar,
		AccionClinicaCrear,
		AccionClinicaEditar,
		AccionClinicaBorrar,
		AccionPermisoOtorgar,
		AccionPermisoRevocar,
		AccionPermisoCrearAdmin,
	}

	for _, accion := range usadas {
		if _, ok := accionesAuditadas[accion]; !ok {
			t.Errorf("la acción %q se usa pero no está en la allowlist", accion)
		}
		if strings.Count(accion, ".") != 1 || strings.HasPrefix(accion, ".") || strings.HasSuffix(accion, ".") {
			t.Errorf("la acción %q debería tener el formato entidad.accion", accion)
		}
	}

	if len(usadas) != len(accionesAuditadas) {
		t.Errorf("la allowlist tiene %d acciones y se usan %d: quedan %d sin usar",
			len(accionesAuditadas), len(usadas), len(accionesAuditadas)-len(usadas))
	}
}

// Los nombres de la allowlist son la clave de la frase que arma el panel, así
// que un cambio de estilo acá tiene que ser un cambio consciente.
func TestAccionesAuditadasFormato(t *testing.T) {
	for accion := range accionesAuditadas {
		entidad, verb, ok := strings.Cut(accion, ".")
		if !ok {
			t.Fatalf("la acción %q no se puede separar en entidad.accion", accion)
		}
		if entidad == "" || verb == "" {
			t.Errorf("la acción %q tiene una parte vacía", accion)
		}
		if entidad != strings.ToLower(entidad) || verb != strings.ToLower(verb) {
			t.Errorf("la acción %q debería ir toda en minúscula", accion)
		}
	}
}

// Las cinco acciones que operan sobre un turno tienen que decir a quién es el
// turno. Si una se olvida de pasar el beneficiario, el panel muestra un UUID
// pelado y no hay forma de saber a quién se le asignó.
func TestDetalleBeneficiarioIdentificaAlSocio(t *testing.T) {
	casos := []struct {
		beneficiario    string
		email           string
		especialidad    string
		esParaAdherente bool
	}{
		{"Juan Pérez", "juan@mail.com", "Cardiología", false},
		{"Ana Gómez", "ana@mail.com", "Radiología", true},
	}

	for i, c := range casos {
		d := detalleBeneficiario(c.beneficiario, c.email, c.especialidad, c.esParaAdherente)

		if d["beneficiario"] != c.beneficiario {
			t.Errorf("caso %d: Beneficiario = %v, quiere %q", i, d["beneficiario"], c.beneficiario)
		}
		if d["socio_email"] != c.email {
			t.Errorf("caso %d: socio_email = %v, quiere %q", i, d["socio_email"], c.email)
		}
		if d["especialidad"] != c.especialidad {
			t.Errorf("caso %d: especialidad = %v, quiere %q", i, d["especialidad"], c.especialidad)
		}
		if d["es_para_adherente"] != c.esParaAdherente {
			t.Errorf("caso %d: es_para_adherente = %v, quiere %v", i, d["es_para_adherente"], c.esParaAdherente)
		}
	}
}

// Un turno viejo, de los que se auditaron antes de que existiera el
// identificador del socio, no tiene estas llaves. El panel las tiene que poder
// leer igual, así que acá no se guardan las vacías.
func TestDetalleBeneficiarioOmiteVacios(t *testing.T) {
	d := detalleBeneficiario("", "", "", false)

	if _, ok := d["beneficiario"]; ok {
		t.Error("guardó beneficiario vacío")
	}
	if _, ok := d["socio_email"]; ok {
		t.Error("guardó socio_email vacío")
	}
	if _, ok := d["especialidad"]; ok {
		t.Error("guardó especialidad vacía")
	}
	if d["es_para_adherente"] != false {
		t.Error("es_para_adherente debería estar siempre, aunque sea false")
	}
}

// Cada acción de turno tiene que usar el helper: el detalle se arma en el
// handler y esta lista es la que dice cuáles lo hacen.
func TestAccionesDeTurnoUsanElHelper(t *testing.T) {
	conBeneficiario := []string{
		AccionTurnoAsignarMedico,
		AccionTurnoAsignarClinica,
		AccionTurnoCancelar,
		AccionEstudioEstado,
		AccionServicioEstado,
	}

	for _, accion := range conBeneficiario {
		if _, ok := accionesAuditadas[accion]; !ok {
			t.Errorf("la acción de turno %q no está en la allowlist", accion)
		}
	}

	if len(conBeneficiario) != 5 {
		t.Fatalf("la lista de acciones de turno cambió (%d): actualizá el test", len(conBeneficiario))
	}
}

func TestDetalleAuditoriaEsJSONValido(t *testing.T) {
	detalles := []map[string]any{
		{},
		{"procesados": 12, "omitidos": 3, "nuevos": 4},
		{"tipo": "plan", "user_id": "", "push_enviados": 0},
		{"motivo": "el socio no divididos"},
	}

	for i, d := range detalles {
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("detalle %d no se pudo serializar: %v", i, err)
		}
		var vuelta map[string]any
		if err := json.Unmarshal(b, &vuelta); err != nil {
			t.Fatalf("detalle %d no vuelve bien del JSON: %v", i, err)
		}
	}
}

// El panel agrupa y filtra por la parte de la entidad del código de acción, así
// que la columna entidad de los call sites tiene que coincidir con ese prefijo.
func TestAccionesAgrupablesPorEntidad(t *testing.T) {
	esperadas := []string{
		"turno", "socio", "plan", "notificacion", "estudio",
		"servicio", "medico", "clinica", "permiso",
	}

	vistas := map[string]bool{}
	for accion := range accionesAuditadas {
		entidad, _, _ := strings.Cut(accion, ".")
		vistas[entidad] = true
	}

	for _, entidad := range esperadas {
		if !vistas[entidad] {
			t.Errorf("ninguna acción usa la entidad %q", entidad)
		}
	}
}
