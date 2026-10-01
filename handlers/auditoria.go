package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aurenbackend/middleware"
)

// Acciones auditadas. El panel las usa para armar la frase legible de cada
// fila, asi que un codigo nuevo tiene que agregarse a la allowlist de abajo.
const (
	AccionSocioCrear          = "socio.crear"
	AccionSocioActualizar     = "socio.actualizar"
	AccionSocioPlanEstado     = "socio.plan_estado"
	AccionSocioBeneficios     = "socio.beneficios"
	AccionSocioImportarCSV    = "socio.importar_csv"
	AccionSocioBackfill       = "socio.backfill"
	AccionNotifCrear          = "notificacion.crear"
	AccionNotifBienvenidas    = "notificacion.bienvenidas"
	AccionPlanUpsert          = "plan.upsert"
	AccionTurnoAsignarMedico  = "turno.asignar_medico"
	AccionTurnoAsignarClinica = "turno.asignar_clinica"
	AccionTurnoCancelar       = "turno.cancelar"
	AccionEstudioEstado       = "estudio.cambiar_estado"
	AccionServicioEstado      = "servicio.cambiar_estado"
	AccionMedicoCrear         = "medico.crear"
	AccionMedicoBorrar        = "medico.borrar"
	AccionClinicaCrear        = "clinica.crear"
	AccionClinicaEditar       = "clinica.editar"
	AccionClinicaBorrar       = "clinica.borrar"
	AccionPermisoOtorgar      = "permiso.otorgar"
	AccionPermisoRevocar      = "permiso.revocar"
	AccionPermisoCrearAdmin   = "permiso.crear_admin"
)

var accionesAuditadas = map[string]struct{}{
	AccionSocioCrear:          {},
	AccionSocioActualizar:     {},
	AccionSocioPlanEstado:     {},
	AccionSocioBeneficios:     {},
	AccionSocioImportarCSV:    {},
	AccionSocioBackfill:       {},
	AccionNotifCrear:          {},
	AccionNotifBienvenidas:    {},
	AccionPlanUpsert:          {},
	AccionTurnoAsignarMedico:  {},
	AccionTurnoAsignarClinica: {},
	AccionTurnoCancelar:       {},
	AccionEstudioEstado:       {},
	AccionServicioEstado:      {},
	AccionMedicoCrear:         {},
	AccionMedicoBorrar:        {},
	AccionClinicaCrear:        {},
	AccionClinicaEditar:       {},
	AccionClinicaBorrar:       {},
	AccionPermisoOtorgar:      {},
	AccionPermisoRevocar:      {},
	AccionPermisoCrearAdmin:   {},
}

// EventoAuditoria es un registro ya persistido, en la forma en que la
// consume el panel y, más adelante, el websocket.
type EventoAuditoria struct {
	ID            int64
	Accion        string
	Entidad       string
	EntidadID     string
	OperadorEmail string
	OperadorRol   string
	Detalle       map[string]any
	Fecha         time.Time
}

// notificarEnVivo es el gancho de la fase C (websocket). Hoy no hace nada:
// la auditoría se escribe en Postgres y nada más. Cuando se agregue
// /api/ws/admin se reemplaza por el broadcast al hub, sin tocar los call
// sites de registrarAuditoria.
var notificarEnVivo = func(ev EventoAuditoria) {}

// detalleBeneficiario es la parte del detalle que identifica a la persona a la
// que pertenece el turno. La usan las cinco acciones que operan sobre un turno:
// sin esto el panel muestra un UUID y no se sabe a quién se le asignó.
//
// Los vacíos no se guardan, así el detalle de un turno viejo (sin estas llaves)
// y el de uno nuevo se pueden leer con el mismo código.
func detalleBeneficiario(beneficiario, email, especialidad string, esParaAdherente bool) map[string]any {
	detalle := map[string]any{"es_para_adherente": esParaAdherente}
	if beneficiario != "" {
		detalle["beneficiario"] = beneficiario
	}
	if email != "" {
		detalle["socio_email"] = email
	}
	if especialidad != "" {
		detalle["especialidad"] = especialidad
	}
	return detalle
}

// registrarAuditoria deja registro de una acción de operador.
//
// El detalle no debe llevar datos sensibles (mensajes de notificación,
// motivos de consulta, contraseñas): la tabla queda consultable desde el
// panel y es permanente.
//
// El INSERT es best-effort y va en goroutine, igual que PGCacheSet: si la
// tabla no existe o Postgres falla, la operación de negocio ya se aplicó y
// no se cae. El error queda en el log del servidor.
func registrarAuditoria(r *http.Request, accion, entidad, entidadID string, detalle map[string]any) {
	if PGPool == nil {
		return
	}

	if _, ok := accionesAuditadas[accion]; !ok {
		log.Printf("[AUDITORIA] acción fuera de la allowlist: %q", accion)
	}

	if detalle == nil {
		detalle = map[string]any{}
	}
	detalleJSON, err := json.Marshal(detalle)
	if err != nil {
		detalleJSON = []byte("{}")
	}

	rol := middleware.RolAdminDefault
	var uid, email string
	if op := middleware.OperadorDesdeContext(r.Context()); op != nil {
		uid, email, rol = op.UID, op.Email, op.Rol
		if op.Rol == "" {
			rol = middleware.RolAdminDefault
		}
	}

	ip := middleware.IPDelRequest(r)
	userAgent := r.UserAgent()

	// Todo lo que usa la goroutine se calcula acá: r no seCaptura porque
	// el handler puede seguir usándolo o descartarlo.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		var id int64
		err := PGPool.QueryRow(ctx, `
			INSERT INTO auditoria_operaciones
				(operador_uid, operador_email, operador_rol, accion, entidad, entidad_id, detalle, ip, user_agent)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id`,
			uid, email, rol, accion, entidad, entidadID, detalleJSON, ip, userAgent,
		).Scan(&id)
		if err != nil {
			log.Printf("[AUDITORIA] error registrando %q: %v", accion, err)
			return
		}

		notificarEnVivo(EventoAuditoria{
			ID:            id,
			Accion:        accion,
			Entidad:       entidad,
			EntidadID:     entidadID,
			OperadorEmail: email,
			OperadorRol:   rol,
			Detalle:       detalle,
			Fecha:         time.Now(),
		})
	}()
}

// ListarAuditoria sirve GET /api/admin/auditoria (RequireAdmin).
// ?pagina=1&porPagina=50&operador=<uid|email>&accion=<codigo>&entidad=<x>&desde=<YYYY-MM-DD>&hasta=<YYYY-MM-DD>
func ListarAuditoria() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		pagina := queryInt(r, "pagina", 1, 1, 100000)
		porPagina := queryInt(r, "porPagina", 50, 1, 200)

		var (
			cond []string
			args []any
		)
		// n agrega un argumento y devuelve su placeholder, así los
		// filtros opcionales no obligan a renumerar $1, $2, ...
		n := func(v any) string {
			args = append(args, v)
			return "$" + strconv.Itoa(len(args))
		}

		q := r.URL.Query()

		if v := strings.TrimSpace(q.Get("operador")); v != "" {
			p := n(v)
			cond = append(cond, "(operador_uid = "+p+" OR operador_email ILIKE "+p+")")
		}
		if v := strings.TrimSpace(q.Get("accion")); v != "" {
			cond = append(cond, "accion = "+n(v))
		}
		if v := strings.TrimSpace(q.Get("entidad")); v != "" {
			cond = append(cond, "entidad = "+n(v))
		}
		// Las fechas inválidas se ignoran en vez de romper el listado.
		if v := strings.TrimSpace(q.Get("desde")); v != "" {
			if t, err := time.Parse("2006-01-02", v); err == nil {
				cond = append(cond, "fecha >= "+n(t))
			}
		}
		if v := strings.TrimSpace(q.Get("hasta")); v != "" {
			if t, err := time.Parse("2006-01-02", v); err == nil {
				cond = append(cond, "fecha < "+n(t.AddDate(0, 0, 1)))
			}
		}

		where := ""
		if len(cond) > 0 {
			where = " WHERE " + strings.Join(cond, " AND ")
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var total int
		if err := PGPool.QueryRow(ctx,
			"SELECT count(*) FROM auditoria_operaciones"+where, args...,
		).Scan(&total); err != nil {
			log.Printf("error contando auditoría: %v", err)
			http.Error(w, "error obteniendo auditoría", http.StatusInternalServerError)
			return
		}

		args = append(args, porPagina, (pagina-1)*porPagina)
		rows, err := PGPool.Query(ctx, fmt.Sprintf(`
			SELECT id,
			       COALESCE(operador_uid, ''), COALESCE(operador_email, ''), COALESCE(operador_rol, ''),
			       accion, entidad, COALESCE(entidad_id, ''), COALESCE(detalle, '{}'::jsonb),
			       COALESCE(ip, ''), COALESCE(user_agent, ''), fecha
			FROM auditoria_operaciones%s
			ORDER BY id DESC
			LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
		if err != nil {
			log.Printf("error listando auditoría: %v", err)
			http.Error(w, "error obteniendo auditoría", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		items := []map[string]any{}
		for rows.Next() {
			var (
				ev        EventoAuditoria
				detalle   []byte
				uid       string
				ip        string
				userAgent string
				fecha     time.Time
			)
			if err := rows.Scan(&ev.ID, &uid, &ev.OperadorEmail, &ev.OperadorRol,
				&ev.Accion, &ev.Entidad, &ev.EntidadID, &detalle,
				&ip, &userAgent, &fecha); err != nil {
				continue
			}
			ev.Detalle = map[string]any{}
			_ = json.Unmarshal(detalle, &ev.Detalle)
			ev.Fecha = fecha

			items = append(items, map[string]any{
				"id":             ev.ID,
				"operador_uid":   uid,
				"operador_email": ev.OperadorEmail,
				"operador_rol":   ev.OperadorRol,
				"accion":         ev.Accion,
				"entidad":        ev.Entidad,
				"entidad_id":     ev.EntidadID,
				"detalle":        ev.Detalle,
				"ip":             ip,
				"user_agent":     userAgent,
				"fecha":          ev.Fecha,
			})
		}

		json.NewEncoder(w).Encode(map[string]any{
			"items":     items,
			"total":     total,
			"pagina":    pagina,
			"porPagina": porPagina,
		})
	}
}

func queryInt(r *http.Request, key string, porDefecto, min, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return porDefecto
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
