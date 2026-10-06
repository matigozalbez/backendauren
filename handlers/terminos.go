package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"aurenbackend/middleware"

	"firebase.google.com/go/v4/auth"
	"github.com/jackc/pgx/v5"
)

// Términos y Condiciones.
//
// Dos piezas: el documento (texto + versión, cargado desde el panel) y la
// aceptación (quién, cuándo, desde dónde, contra qué texto exacto).
//
// El gate es por VERSIÓN, no "una vez para siempre": mientras no haya
// documento publicado no se bloquea a nadie (no hay nada que aceptar), y en
// cuanto sale una versión, la tienen que aceptar todos los que no tengan esa
// versión registrada. Si mañana cambiás el texto, se pide de nuevo y el
// registro de la versión vieja queda intacto.
//
// La identidad nunca sale del body. Hay dos caminos, y el backend decide
// cuál usó:
//
//   - Con Bearer (ya logueado con Google, o vinculado): uid -> socios.
//     Es el modo que va después de que la cuenta ya existe.
//
//   - Sin Bearer (primer ingreso, todavía no hay cuenta): el DNI del body
//     tiene que tener codigos_verificacion.verificado = TRUE, que es
//     exactamente el mismo requisito que exige CrearPassword para crear la
//     contraseña. Quien llegó hasta acá recibió el código en el mail del
//     socio. Ese registro se borra al crear la cuenta, así que la ventana
//     coincide con el flujo.

var errSinTerminos = errors.New("no hay términos publicados")

// rechazoTerminos es un error que ya sabe con qué status responder. Sin esto
// el handler tendría que adivinar si el fallo es un 401 (token presente pero
// inválido) o un 403 (la petición está bien formada y este socio no puede
// hacerla), mirando el header, que es lo mismo que hacer por string.
type rechazoTerminos struct {
	codigo int
	msg    string
}

func (e *rechazoTerminos) Error() string { return e.msg }

func rechazar(codigo int, formato string, args ...any) error {
	return &rechazoTerminos{codigo: codigo, msg: fmt.Sprintf(formato, args...)}
}

// Orígenes aceptados en la columna origen. No es una columna con CHECK para
// que agregar uno nuevo no requiera migración, pero sí se valida acá para que
// no entre cualquier string libre.
var origenesAceptacion = map[string]struct{}{
	"primer_ingreso": {},
	"google":         {},
	"email":          {},
}

// TerminosDocumento es una fila de terminos_documentos.
type TerminosDocumento struct {
	ID       int64     `json:"id"`
	Version  string    `json:"version"`
	Titulo   string    `json:"titulo"`
	Cuerpo   string    `json:"cuerpo"`
	CreadoEn time.Time `json:"creadoEn"`
}

// terminosVigente devuelve el documento activo. errSinTerminos si todavía no
// cargaron ninguno: el gate lo trata como "no hay gate", no como un error.
func terminosVigente(ctx context.Context) (*TerminosDocumento, error) {
	doc := &TerminosDocumento{}
	err := PGPool.QueryRow(ctx, `
		SELECT id, version, titulo, cuerpo, creado_en
		FROM terminos_documentos
		WHERE activo = TRUE
		ORDER BY creado_en DESC
		LIMIT 1`,
	).Scan(&doc.ID, &doc.Version, &doc.Titulo, &doc.Cuerpo, &doc.CreadoEn)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errSinTerminos
	}
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// hashDocumento es la prueba de qué texto vio el socio. Dos versiones con el
// mismo cuerpo dan el mismo hash, y un cuerpo editado por fuera del panel da
// otro: si alguien toca la tabla directo, la evidencia no cierra.
func hashDocumento(cuerpo string) string {
	h := sha256.Sum256([]byte(cuerpo))
	return hex.EncodeToString(h[:])
}

// puedeAceptarTerminos deja pasar a un socio a aceptar. Devuelve el socio y
// el uid ("" si el flujo es sin cuenta todavía).
func puedeAceptarTerminos(ctx context.Context, r *http.Request, authClient *auth.Client, dniBody string) (*SocioRecord, string, error) {
	if r.Header.Get("Authorization") != "" {
		// Ya está logueado (Google, o volvió a /terminos con sesión).
		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			return nil, "", rechazar(http.StatusUnauthorized, "no autorizado, iniciá sesión")
		}
		socio, err := leerSocioPorUID(ctx, uid)
		if err != nil {
			return nil, "", rechazar(http.StatusForbidden, "tu cuenta no está vinculada a ningún socio")
		}
		return socio, uid, nil
	}

	// Primer ingreso: todavía no existe la cuenta en Auth.
	if !reDNI.MatchString(dniBody) {
		return nil, "", rechazar(http.StatusBadRequest, "DNI inválido")
	}

	var verificado bool
	err := PGPool.QueryRow(ctx,
		`SELECT verificado FROM codigos_verificacion WHERE dni = $1`, dniBody,
	).Scan(&verificado)
	if err != nil || !verificado {
		return nil, "", rechazar(http.StatusForbidden, "verificá tu código primero")
	}

	socio, err := leerSocioPorDNI(ctx, dniBody)
	if err != nil {
		return nil, "", rechazar(http.StatusForbidden, "ese DNI no pertenece a ningún socio")
	}
	return socio, "", nil
}

// AceptarTerminos sirve POST /api/terminos/aceptar. Acepta los dos modos de
// identificación de puedeAceptarTerminos. Es idempotente: volver a aceptar la
// misma versión no rompe, solo setea el uid si faltaba.
func AceptarTerminos(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "metodo no permitido", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			DNI     string `json:"dni"`
			Version string `json:"version"`
			Acepto  *bool  `json:"acepto"`
			Origen  string `json:"origen"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "JSON invalido", http.StatusBadRequest)
			return
		}
		if req.Acepto == nil || !*req.Acepto {
			http.Error(w, "tenés que aceptar los Términos y Condiciones", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		vigente, err := terminosVigente(ctx)
		if err != nil {
			if errors.Is(err, errSinTerminos) {
				// No se puede aceptar algo que no existe. El front no debería
				// llegar acá (si no hay documento, no hay gate).
				http.Error(w, "no hay términos publicados", http.StatusNotFound)
				return
			}
			log.Printf("[terminos] ERROR leyendo documento vigente: %v", err)
			http.Error(w, "error leyendo los términos", http.StatusInternalServerError)
			return
		}

		// La versión que mandó el front es la que VIÓ. Si no coincide con la
		// vigente, el socio está aceptando un texto que ya no es el actual y
		// el registro quedaría sin valor probatorio.
		if req.Version != "" && req.Version != vigente.Version {
			http.Error(w, "los términos cambiaron, recargá la página", http.StatusConflict)
			return
		}

		origen := strings.TrimSpace(req.Origen)
		if origen == "" {
			origen = "email"
		}
		if _, ok := origenesAceptacion[origen]; !ok {
			origen = "email"
		}

		socio, uid, err := puedeAceptarTerminos(ctx, r, authClient, req.DNI)
		if err != nil {
			codigo := http.StatusForbidden
			var rechazo *rechazoTerminos
			if errors.As(err, &rechazo) {
				codigo = rechazo.codigo
			}
			// El mensaje del error puede venir de leerSocioPorDNI, así que se
			// manda tal cual: es un texto nuestro, no err.Error() de un paquete.
			http.Error(w, err.Error(), codigo)
			return
		}

		hash := hashDocumento(vigente.Cuerpo)

		// ON CONFLICT en vez de fallar: si el socio acepta dos veces (recargó
		// la pantalla), la fila sigue siendo única y lo único que se completa
		// es el uid, que en el primer ingreso todavía estaba en NULL.
		_, err = PGPool.Exec(ctx, `
			INSERT INTO aceptaciones_terminos
				(dni, uid, nombre, apellido, terminos_version, documento_hash,
				 ip, user_agent, origen)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (dni, terminos_version) DO UPDATE SET
				uid = COALESCE(aceptaciones_terminos.uid, EXCLUDED.uid)`,
			socio.DNI,
			uid,
			socio.Nombre,
			socio.Apellido,
			vigente.Version,
			hash,
			middleware.IPDelRequest(r),
			r.UserAgent(),
			origen,
		)
		if err != nil {
			log.Printf("[terminos] ERROR guardando aceptacion de dni %s: %v", socio.DNI, err)
			http.Error(w, "error registrando tu aceptación", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"acepto":        true,
			"version":       vigente.Version,
			"documentoHash": hash,
		})
	}
}

// TerminosVigente sirve GET /api/terminos/vigente. Pública: el front necesita
// el texto antes de que el socio esté logueado (y antes de que exista la
// cuenta). No lleva token porque así no se filtra que un DNI está en el
// padrón.
func TerminosVigente(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "metodo no permitido", http.StatusMethodNotAllowed)
		return
	}

	vigente, err := terminosVigente(context.Background())
	if err != nil {
		if errors.Is(err, errSinTerminos) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]bool{"sinTerminos": true})
			return
		}
		log.Printf("[terminos] ERROR leyendo vigente: %v", err)
		http.Error(w, "error leyendo los términos", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(vigente)
}

// MisTerminos sirve GET /api/terminos/mi-aceptacion (con Bearer). Le dice al
// front si este socio ya aceptó la versión vigente, para no pedirle de nuevo.
func MisTerminos(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "metodo no permitido", http.StatusMethodNotAllowed)
			return
		}

		uid, err := verifyIDToken(r, authClient)
		if err != nil {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}

		ctx := context.Background()
		vigente, err := terminosVigente(ctx)
		if err != nil {
			if errors.Is(err, errSinTerminos) {
				json.NewEncoder(w).Encode(map[string]any{"sinTerminos": true})
				return
			}
			log.Printf("[terminos] ERROR leyendo vigente: %v", err)
			http.Error(w, "error leyendo los términos", http.StatusInternalServerError)
			return
		}

		socio, err := leerSocioPorUID(ctx, uid)
		if err != nil {
			// Sin socio vinculado todavía no hay a quién cobrarle los términos.
			json.NewEncoder(w).Encode(map[string]any{
				"version":    vigente.Version,
				"vinculado":  false,
				"acepto":     false,
				"aceptadoEn": nil,
			})
			return
		}

		var aceptadoEn *time.Time
		_ = PGPool.QueryRow(ctx, `
			SELECT aceptado_en FROM aceptaciones_terminos
			WHERE dni = $1 AND terminos_version = $2`,
			socio.DNI, vigente.Version,
		).Scan(&aceptadoEn)

		respuesta := map[string]any{
			"version":    vigente.Version,
			"vinculado":  true,
			"acepto":     aceptadoEn != nil,
			"aceptadoEn": nil,
		}
		if aceptadoEn != nil {
			respuesta["aceptadoEn"] = aceptadoEn
		}
		json.NewEncoder(w).Encode(respuesta)
	}
}

// TerminosAceptadosParaDNI es el gate del backend, usado por CrearPassword.
// Devuelve true si no hay documento publicado: no se puede exigir aceptar un
// texto que todavía no existe.
func TerminosAceptadosParaDNI(ctx context.Context, dni string) (bool, error) {
	vigente, err := terminosVigente(ctx)
	if err != nil {
		if errors.Is(err, errSinTerminos) {
			return true, nil
		}
		return false, err
	}
	var ok bool
	if err := PGPool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM aceptaciones_terminos
			WHERE dni = $1 AND terminos_version = $2
		)`, dni, vigente.Version,
	).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// responderTerminosPendientes es el 403 que el front traduce en el modal de
// términos: el socio sigue usando la app, pero ningún servicio avanza hasta
// aceptar la versión vigente.
func responderTerminosPendientes(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]any{
		"codigo":  "TERMINOS_NO_ACEPTADOS",
		"mensaje": "Para usar este servicio tenés que aceptar los Términos y Condiciones vigentes",
	})
}

// gateTerminosServicios aborta la creación de turnos, estudios y solicitudes
// (grúa, sepelios, médico a domicilio) cuando el socio no tiene aceptada la
// versión vigente de los T&C. Devuelve true si se puede seguir, false si ya
// respondió (403 de términos o error interno). Se evalúa con el DNI que el
// backend ya resolvió del uid, nunca con datos del body.
func gateTerminosServicios(ctx context.Context, w http.ResponseWriter, dni string) bool {
	acepto, err := TerminosAceptadosParaDNI(ctx, dni)
	if err != nil {
		log.Printf("[terminos] ERROR gate de servicios dni %s: %v", dni, err)
		http.Error(w, "error verificando los términos y condiciones", http.StatusInternalServerError)
		return false
	}
	if !acepto {
		responderTerminosPendientes(w)
		return false
	}
	return true
}

// gateTerminosServiciosUID es la variante que arranca del uid, para los
// handlers donde todavía no se leyó el socio (subir imagen de estudio).
func gateTerminosServiciosUID(ctx context.Context, w http.ResponseWriter, uid string) bool {
	socio, err := leerSocioPorUID(ctx, uid)
	if err != nil {
		http.Error(w, "no se encontró el socio asociado a esta cuenta", http.StatusNotFound)
		return false
	}
	return gateTerminosServicios(ctx, w, socio.DNI)
}

// marcarUIDAceptacion completa el uid en la aceptación que quedó en NULL:
// en el primer ingreso el socio acepta ANTES de que exista la cuenta, así que
// la fila se crea sin uid y este UPDATE llega después, cuando CrearPassword o
// VincularSocio ya resolvió el uid de Firebase. Best-effort: si falla no se
// cae la creación de la cuenta, queda el registro igual (enlazable por DNI).
func marcarUIDAceptacion(ctx context.Context, dni, uid string) {
	if _, err := PGPool.Exec(ctx, `
		UPDATE aceptaciones_terminos SET uid = $2
		WHERE dni = $1 AND uid IS NULL`, dni, uid); err != nil {
		log.Printf("[terminos] ERROR seteando uid en aceptacion de dni %s: %v", dni, err)
	}
}

// PublicarTerminos sirve POST /api/admin/terminos (RequireOperador).
//
// Desactiva las versiones anteriores y activa la nueva en la misma
// transacción: si algo falla a mitad de camino no queda ninguna activa ni dos.
// Es la única forma de cambiar el texto, así que la invariante de "una sola
// activa" no depende de que nadie escriba SQL a mano.
func PublicarTerminos() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Version string `json:"version"`
			Titulo  string `json:"titulo"`
			Cuerpo  string `json:"cuerpo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		req.Version = strings.TrimSpace(req.Version)
		req.Titulo = strings.TrimSpace(req.Titulo)
		req.Cuerpo = strings.TrimSpace(req.Cuerpo)

		if req.Version == "" {
			http.Error(w, "falta la versión", http.StatusBadRequest)
			return
		}
		if req.Cuerpo == "" {
			http.Error(w, "falta el cuerpo de los términos", http.StatusBadRequest)
			return
		}
		if req.Titulo == "" {
			req.Titulo = "Términos y Condiciones"
		}
		if len(req.Cuerpo) > 500000 {
			http.Error(w, "los términos son demasiado largos", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		tx, err := PGPool.Begin(ctx)
		if err != nil {
			log.Printf("[terminos] ERROR abriendo transacción: %v", err)
			http.Error(w, "error publicando los términos", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback(ctx)

		if _, err := tx.Exec(ctx,
			`UPDATE terminos_documentos SET activo = FALSE, actualizado_en = now() WHERE activo = TRUE`); err != nil {
			log.Printf("[terminos] ERROR desactivando versiones previas: %v", err)
			http.Error(w, "error publicando los términos", http.StatusInternalServerError)
			return
		}

		var id int64
		var creadoEn time.Time
		if err := tx.QueryRow(ctx, `
			INSERT INTO terminos_documentos (version, titulo, cuerpo, activo)
			VALUES ($1, $2, $3, TRUE)
			RETURNING id, creado_en`,
			req.Version, req.Titulo, req.Cuerpo,
		).Scan(&id, &creadoEn); err != nil {
			// El índice único parcial es el que dispara si ya hay una activa,
			// y una versión repetida da el UNIQUE de la tabla.
			log.Printf("[terminos] ERROR insertando version %q: %v", req.Version, err)
			http.Error(w, "no se pudo publicar: esa versión ya existe", http.StatusConflict)
			return
		}

		if err := tx.Commit(ctx); err != nil {
			log.Printf("[terminos] ERROR confirmando publicacion: %v", err)
			http.Error(w, "error publicando los términos", http.StatusInternalServerError)
			return
		}

		registrarAuditoria(r, AccionTerminosPublicar, "terminos", req.Version, map[string]any{
			"titulo": req.Titulo,
			"hash":   hashDocumento(req.Cuerpo),
			"largo":  len(req.Cuerpo),
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":       id,
			"version":  req.Version,
			"titulo":   req.Titulo,
			"hash":     hashDocumento(req.Cuerpo),
			"creadoEn": creadoEn,
		})
	}
}
