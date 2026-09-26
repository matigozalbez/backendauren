package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// Acceso a la tabla "socios" (reemplaza la colección Firestore homónima).
// Los campos anidados (planes, adherentes, beneficios) viven en columnas JSONB.

type SocioRecord struct {
	DNI                   string
	UID                   *string
	Nombre                string
	Apellido              string
	Email                 string
	Telefono              string
	Edad                  string
	Provincia             string
	Ciudad                string
	Direccion             string
	MetodoPago            string
	CBU                   string
	TarjetaUltimosDigitos string
	TarjetaVencimiento    string
	Planes                []byte
	Estado                string
	Adherentes            []byte
	Beneficios            []byte
}

const columnasSocio = `
	dni, uid, nombre, apellido, email, telefono, edad,
	provincia, ciudad, direccion, metodo_pago, cbu,
	tarjeta_ultimos_digitos, tarjeta_vencimiento,
	planes, estado, adherentes, beneficios`

func scanSocio(row interface{ Scan(...interface{}) error }) (*SocioRecord, error) {
	s := &SocioRecord{}
	// Casi todas las columnas de "socios" son NULLABLE (el importador de CSV y
	// los inserts parciales no las llenan). Con sql.NullString evitamos que un
	// solo NULL reviente el Scan y el socio desaparezca del listado / app.
	var email, telefono, edad, provincia, ciudad, direccion sql.NullString
	var metodoPago, cbu, tarjetaUltimos, tarjetaVenc sql.NullString
	var uid sql.NullString
	var planes, adherentes, beneficios sql.NullString

	err := row.Scan(
		&s.DNI, &uid, &s.Nombre, &s.Apellido, &email, &telefono, &edad,
		&provincia, &ciudad, &direccion, &metodoPago, &cbu,
		&tarjetaUltimos, &tarjetaVenc,
		&planes, &s.Estado, &adherentes, &beneficios,
	)
	if err != nil {
		return nil, err
	}

	if uid.Valid {
		s.UID = &uid.String
	}
	s.Email = email.String
	s.Telefono = telefono.String
	s.Edad = edad.String
	s.Provincia = provincia.String
	s.Ciudad = ciudad.String
	s.Direccion = direccion.String
	s.MetodoPago = metodoPago.String
	s.CBU = cbu.String
	s.TarjetaUltimosDigitos = tarjetaUltimos.String
	s.TarjetaVencimiento = tarjetaVenc.String
	s.Planes = []byte(planes.String)
	s.Adherentes = []byte(adherentes.String)
	s.Beneficios = []byte(beneficios.String)

	return s, nil
}

func leerSocioPorDNI(ctx context.Context, dni string) (*SocioRecord, error) {
	return scanSocio(PGPool.QueryRow(ctx,
		"SELECT "+columnasSocio+" FROM socios WHERE dni = $1", dni))
}

func leerSocioPorUID(ctx context.Context, uid string) (*SocioRecord, error) {
	return scanSocio(PGPool.QueryRow(ctx,
		"SELECT "+columnasSocio+" FROM socios WHERE uid = $1 LIMIT 1", uid))
}

// UIDStr devuelve el UID vinculado ("" si todavía no tiene cuenta).
func (s *SocioRecord) UIDStr() string {
	if s.UID == nil {
		return ""
	}
	return *s.UID
}

// Data devuelve el socio como map con los mismos tipos que devolvía Firestore,
// para mantener intacta la lógica que trabaja contra data["planes"], etc.
func (s *SocioRecord) Data() map[string]interface{} {
	data := map[string]interface{}{
		"dni":                   s.DNI,
		"nombre":                s.Nombre,
		"apellido":              s.Apellido,
		"email":                 s.Email,
		"telefono":              s.Telefono,
		"edad":                  s.Edad,
		"provincia":             s.Provincia,
		"ciudad":                s.Ciudad,
		"direccion":             s.Direccion,
		"metodoPago":            s.MetodoPago,
		"cbu":                   s.CBU,
		"tarjetaUltimosDigitos": s.TarjetaUltimosDigitos,
		"tarjetaVencimiento":    s.TarjetaVencimiento,
		"estado":                s.Estado,
	}

	if s.UID != nil {
		data["uid"] = *s.UID
	} else {
		data["uid"] = nil
	}

	var planes []interface{}
	_ = json.Unmarshal(s.Planes, &planes)
	data["planes"] = planes

	var adherentes []interface{}
	_ = json.Unmarshal(s.Adherentes, &adherentes)
	data["adherentes"] = adherentes

	var beneficios map[string]interface{}
	_ = json.Unmarshal(s.Beneficios, &beneficios)
	if beneficios == nil {
		beneficios = map[string]interface{}{}
	}
	data["beneficios"] = beneficios

	return data
}

// jsonbSociosPlanes serializa []PlanSocio para la columna JSONB planes.
func jsonbSociosPlanes(planes []PlanSocio) ([]byte, error) {
	items := make([]map[string]interface{}, 0, len(planes))
	for _, p := range planes {
		estado := p.Estado
		if estado == "" {
			estado = "activo"
		}
		items = append(items, map[string]interface{}{
			"nombre": p.Nombre,
			"estado": estado,
		})
	}
	return json.Marshal(items)
}

// jsonbSociosAdherentes serializa []AdherenteInput para la columna JSONB adherentes.
func jsonbSociosAdherentes(adherentes []AdherenteInput) ([]byte, error) {
	items := make([]map[string]interface{}, 0, len(adherentes))
	for _, a := range adherentes {
		items = append(items, map[string]interface{}{
			"relacion": a.Relacion,
			"nombre":   a.Nombre,
			"apellido": a.Apellido,
			"dni":      a.DNI,
			"edad":     a.Edad,
		})
	}
	return json.Marshal(items)
}

// socioCSVRow es una fila ya validada del importador de CSV.
type socioCSVRow struct {
	dni       string
	nombre    string
	apellido  string
	email     string
	telefono  string
	direccion string
	planes    []map[string]interface{}
	estado    string
}

// upsertSociosCSV inserta por lotes los socios del CSV. Si el DNI ya existe,
// actualiza solo los campos del CSV (respeta lo demás, como hacía MergeAll).
func upsertSociosCSV(ctx context.Context, filas []socioCSVRow) error {
	const batchSize = 500

	for i := 0; i < len(filas); i += batchSize {
		end := i + batchSize
		if end > len(filas) {
			end = len(filas)
		}
		chunk := filas[i:end]

		placeholders := make([]string, 0, len(chunk))
		args := make([]interface{}, 0, len(chunk)*8)
		for j, f := range chunk {
			base := j * 8
			placeholders = append(placeholders, fmt.Sprintf(
				"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8,
			))
			planesJSON, err := json.Marshal(f.planes)
			if err != nil {
				return err
			}
			args = append(args, f.dni, f.nombre, f.apellido, f.email,
				f.telefono, f.direccion, planesJSON, f.estado)
		}

		query := `INSERT INTO socios (dni, nombre, apellido, email, telefono, direccion, planes, estado) VALUES ` +
			strings.Join(placeholders, ",") +
			` ON CONFLICT (dni) DO UPDATE SET
				nombre = EXCLUDED.nombre,
				apellido = EXCLUDED.apellido,
				email = EXCLUDED.email,
				telefono = EXCLUDED.telefono,
				direccion = EXCLUDED.direccion,
				planes = EXCLUDED.planes,
				estado = EXCLUDED.estado,
				actualizado_en = now()`

		if _, err := PGPool.Exec(ctx, query, args...); err != nil {
			log.Printf("ERROR en upsert de socios por CSV: %v", err)
			return err
		}
	}
	return nil
}