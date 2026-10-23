package handlers

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"firebase.google.com/go/v4/auth"
)

type CocheriaInput struct {
	Nombre       string `json:"nombre"`
	Domicilio    string `json:"domicilio"`
	Localidad    string `json:"localidad"`
	CodigoPostal string `json:"codigoPostal"`
	Telefono     string `json:"telefono"`
}

type CocheriaView struct {
	ID           int    `json:"id"`
	Nombre       string `json:"nombre"`
	Domicilio    string `json:"domicilio"`
	Localidad    string `json:"localidad"`
	CodigoPostal string `json:"codigoPostal"`
	Telefono     string `json:"telefono"`
}

type CocheriasPage struct {
	Items        []CocheriaView `json:"items"`
	Total        int            `json:"total"`
	Pagina       int            `json:"pagina"`
	PorPagina    int            `json:"porPagina"`
	TotalPaginas int            `json:"totalPaginas"`
}

func ListarCocherias(authClient *auth.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}
		if _, err := verifyIDToken(r, authClient); err != nil {
			http.Error(w, "no autorizado, iniciá sesión", http.StatusUnauthorized)
			return
		}
		listarCocherias(w, r)
	}
}

func ListarCocheriasAdmin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}
		listarCocherias(w, r)
	}
}

func listarCocherias(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	pagina, porPagina := paginacionCocherias(r)
	busqueda := strings.TrimSpace(r.URL.Query().Get("q"))
	patron := "%" + busqueda + "%"

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var total int
	if err := PGPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM cocherias
		WHERE $1::text = ''
		   OR nombre ILIKE $2::text
		   OR localidad ILIKE $2::text
		   OR codigo_postal ILIKE $2::text`,
		busqueda, patron,
	).Scan(&total); err != nil {
		log.Printf("ERROR contando cocherías: %v", err)
		http.Error(w, "error leyendo las cocherías", http.StatusInternalServerError)
		return
	}

	totalPaginas := 0
	if total > 0 {
		totalPaginas = (total + porPagina - 1) / porPagina
	}
	if totalPaginas > 0 && pagina > totalPaginas {
		pagina = totalPaginas
	}

	rows, err := PGPool.Query(ctx, `
		SELECT id, COALESCE(nombre,''), COALESCE(domicilio,''),
		       COALESCE(localidad,''), COALESCE(codigo_postal,''), COALESCE(telefono,'')
		FROM cocherias
		WHERE $1::text = ''
		   OR nombre ILIKE $2::text
		   OR localidad ILIKE $2::text
		   OR codigo_postal ILIKE $2::text
		ORDER BY nombre ASC, localidad ASC, id ASC
		LIMIT $3 OFFSET $4`,
		busqueda, patron, porPagina, (pagina-1)*porPagina,
	)
	if err != nil {
		log.Printf("ERROR listando cocherías: %v", err)
		http.Error(w, "error leyendo las cocherías", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := make([]CocheriaView, 0)
	for rows.Next() {
		var c CocheriaView
		if err := rows.Scan(&c.ID, &c.Nombre, &c.Domicilio, &c.Localidad, &c.CodigoPostal, &c.Telefono); err != nil {
			log.Printf("ERROR leyendo cochería: %v", err)
			continue
		}
		items = append(items, c)
	}

	json.NewEncoder(w).Encode(CocheriasPage{
		Items:        items,
		Total:        total,
		Pagina:       pagina,
		PorPagina:    porPagina,
		TotalPaginas: totalPaginas,
	})
}

func paginacionCocherias(r *http.Request) (int, int) {
	pagina := 1
	porPagina := 20

	if v, err := strconv.Atoi(r.URL.Query().Get("pagina")); err == nil && v > 0 {
		pagina = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("porPagina")); err == nil && v > 0 {
		porPagina = v
	}
	if porPagina > 100 {
		porPagina = 100
	}
	return pagina, porPagina
}

func CrearCocheria() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		input, ok := decodificarCocheria(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var id int
		if err := PGPool.QueryRow(ctx, `
			INSERT INTO cocherias (nombre, domicilio, localidad, codigo_postal, telefono)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id`,
			input.Nombre, input.Domicilio, input.Localidad, input.CodigoPostal, input.Telefono,
		).Scan(&id); err != nil {
			log.Printf("ERROR creando cochería %s: %v", input.Nombre, err)
			http.Error(w, "error guardando la cochería", http.StatusInternalServerError)
			return
		}

		registrarAuditoria(r, AccionCocheriaCrear, "cocheria", strconv.Itoa(id), map[string]any{
			"nombre":    input.Nombre,
			"localidad": input.Localidad,
		})

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "id": id})
	}
}

func EditarCocheria() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var body struct {
			ID int `json:"id"`
			CocheriaInput
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		input := normalizarCocheriaInput(body.CocheriaInput)
		if body.ID == 0 || input.Nombre == "" {
			http.Error(w, "faltan datos", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		tag, err := PGPool.Exec(ctx, `
			UPDATE cocherias SET
				nombre = $1,
				domicilio = $2,
				localidad = $3,
				codigo_postal = $4,
				telefono = $5
			WHERE id = $6`,
			input.Nombre, input.Domicilio, input.Localidad, input.CodigoPostal, input.Telefono, body.ID,
		)
		if err != nil {
			log.Printf("ERROR actualizando cochería %d: %v", body.ID, err)
			http.Error(w, "error actualizando la cochería", http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			http.Error(w, "cochería no encontrada", http.StatusNotFound)
			return
		}

		registrarAuditoria(r, AccionCocheriaEditar, "cocheria", strconv.Itoa(body.ID), map[string]any{
			"nombre":    input.Nombre,
			"localidad": input.Localidad,
		})

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func BorrarCocheria() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		var input struct {
			ID int `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.ID == 0 {
			http.Error(w, "id requerido", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		tag, err := PGPool.Exec(ctx, `DELETE FROM cocherias WHERE id = $1`, input.ID)
		if err != nil {
			log.Printf("ERROR borrando cochería %d: %v", input.ID, err)
			http.Error(w, "error al borrar la cochería", http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			http.Error(w, "cochería no encontrada", http.StatusNotFound)
			return
		}

		registrarAuditoria(r, AccionCocheriaBorrar, "cocheria", strconv.Itoa(input.ID), map[string]any{})

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

func ImportarCocherias() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "no se pudo leer el archivo", http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("archivo")
		if err != nil {
			http.Error(w, "falta el archivo", http.StatusBadRequest)
			return
		}
		defer file.Close()

		size := header.Size
		if size <= 0 {
			if seeker, ok := file.(io.Seeker); ok {
				if current, err := seeker.Seek(0, io.SeekCurrent); err == nil {
					if end, err := seeker.Seek(0, io.SeekEnd); err == nil {
						size = end - current
						_, _ = seeker.Seek(current, io.SeekStart)
					}
				}
			}
		}
		if size <= 0 {
			http.Error(w, "el archivo está vacío", http.StatusBadRequest)
			return
		}

		readerAt, ok := file.(io.ReaderAt)
		if !ok {
			http.Error(w, "no se pudo leer el archivo", http.StatusBadRequest)
			return
		}

		filas, err := leerXLSX(readerAt, size)
		if err != nil {
			log.Printf("ERROR leyendo Excel de cocherías: %v", err)
			http.Error(w, "el archivo no es un Excel válido", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		tx, err := PGPool.Begin(ctx)
		if err != nil {
			log.Printf("ERROR iniciando importación de cocherías: %v", err)
			http.Error(w, "error importando las cocherías", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback(ctx)

		importadas := 0
		omitidas := 0
		vistas := make(map[string]struct{})

		for _, fila := range filas {
			input, ok := filaCocheria(fila)
			if !ok {
				omitidas++
				continue
			}

			clave := strings.ToLower(strings.Join([]string{
				input.Nombre, input.Domicilio, input.Localidad, input.CodigoPostal, input.Telefono,
			}, "|"))
			if _, repetida := vistas[clave]; repetida {
				omitidas++
				continue
			}
			vistas[clave] = struct{}{}

			if _, err := tx.Exec(ctx, `
				INSERT INTO cocherias (nombre, domicilio, localidad, codigo_postal, telefono)
				VALUES ($1, $2, $3, $4, $5)`,
				input.Nombre, input.Domicilio, input.Localidad, input.CodigoPostal, input.Telefono,
			); err != nil {
				log.Printf("ERROR importando cochería %q: %v", input.Nombre, err)
				http.Error(w, "error importando las cocherías", http.StatusInternalServerError)
				return
			}
			importadas++
		}

		if err := tx.Commit(ctx); err != nil {
			log.Printf("ERROR confirmando importación de cocherías: %v", err)
			http.Error(w, "error importando las cocherías", http.StatusInternalServerError)
			return
		}

		registrarAuditoria(r, AccionCocheriaImportar, "cocheria", "", map[string]any{
			"archivo":    header.Filename,
			"importadas": importadas,
			"omitidas":   omitidas,
		})

		json.NewEncoder(w).Encode(map[string]any{
			"status":     "ok",
			"importadas": importadas,
			"omitidas":   omitidas,
		})
	}
}

func decodificarCocheria(w http.ResponseWriter, r *http.Request) (CocheriaInput, bool) {
	var input CocheriaInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return input, false
	}

	input = normalizarCocheriaInput(input)
	if input.Nombre == "" {
		http.Error(w, "el nombre es obligatorio", http.StatusBadRequest)
		return input, false
	}
	return input, true
}

func normalizarCocheriaInput(input CocheriaInput) CocheriaInput {
	input.Nombre = limpiarCelda(input.Nombre)
	input.Domicilio = limpiarCelda(input.Domicilio)
	input.Localidad = limpiarCelda(input.Localidad)
	input.CodigoPostal = limpiarCelda(input.CodigoPostal)
	input.Telefono = limpiarCelda(input.Telefono)
	return input
}

func filaCocheria(fila []string) (CocheriaInput, bool) {
	valor := func(i int) string {
		if i >= len(fila) {
			return ""
		}
		return limpiarCelda(fila[i])
	}

	input := CocheriaInput{
		Nombre:       valor(0),
		Domicilio:    valor(1),
		Localidad:    valor(2),
		CodigoPostal: valor(3),
		Telefono:     valor(4),
	}

	if input.Nombre == "" {
		return input, false
	}
	if strings.EqualFold(input.Nombre, "cochería") || strings.EqualFold(input.Nombre, "cocheria") {
		return input, false
	}
	if strings.EqualFold(input.Domicilio, "domicilio") && strings.EqualFold(input.Localidad, "localidad") {
		return input, false
	}
	if input.Domicilio == "" && input.Localidad == "" && input.CodigoPostal == "" && input.Telefono == "" {
		return input, false
	}

	return input, true
}

func limpiarCelda(valor string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(valor)), " ")
}

type xlsxWorkbook struct {
	Sheets []struct {
		Name string `xml:"name,attr"`
		RID  string `xml:"id,attr"`
	} `xml:"sheets>sheet"`
}

type xlsxRelationships struct {
	Items []struct {
		ID     string `xml:"Id,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

type xlsxRichText struct {
	Text string `xml:"t"`
	Runs []struct {
		Text string `xml:"t"`
	} `xml:"r"`
}

type xlsxSharedStrings struct {
	Items []xlsxRichText `xml:"si"`
}

type xlsxWorksheet struct {
	Rows []struct {
		R     string `xml:"r,attr"`
		Cells []struct {
			Ref    string        `xml:"r,attr"`
			Type   string        `xml:"t,attr"`
			Value  string        `xml:"v"`
			Inline *xlsxRichText `xml:"is"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

func leerXLSX(reader io.ReaderAt, size int64) ([][]string, error) {
	zr, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, err
	}

	compartidos, _ := leerCompartidosXLSX(zr)
	hoja, err := leerHojaXLSX(zr)
	if err != nil {
		return nil, err
	}

	var ws xlsxWorksheet
	if err := xml.Unmarshal(hoja, &ws); err != nil {
		return nil, err
	}

	celdas := make(map[int]map[int]string)
	maxFila := 0
	maxColumna := 0

	for i, fila := range ws.Rows {
		numeroFila, _ := strconv.Atoi(fila.R)
		if numeroFila <= 0 {
			numeroFila = i + 1
		}
		indiceFila := numeroFila - 1

		if indiceFila+1 > maxFila {
			maxFila = indiceFila + 1
		}

		for j, celda := range fila.Cells {
			indiceColumna, ok := columnaDeReferencia(celda.Ref)
			if !ok {
				indiceColumna = j
			}
			if indiceColumna+1 > maxColumna {
				maxColumna = indiceColumna + 1
			}

			valor := valorCeldaXLSX(celda.Type, celda.Value, celda.Inline, compartidos)
			if valor == "" {
				continue
			}
			if celdas[indiceFila] == nil {
				celdas[indiceFila] = make(map[int]string)
			}
			celdas[indiceFila][indiceColumna] = valor
		}
	}

	filas := make([][]string, maxFila)
	for i := 0; i < maxFila; i++ {
		filas[i] = make([]string, maxColumna)
		for j, valor := range celdas[i] {
			if j >= 0 && j < maxColumna {
				filas[i][j] = valor
			}
		}
	}

	return filas, nil
}

func leerCompartidosXLSX(zr *zip.Reader) ([]string, error) {
	data, err := leerArchivoZip(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil, err
	}

	var sst xlsxSharedStrings
	if err := xml.Unmarshal(data, &sst); err != nil {
		return nil, err
	}

	items := make([]string, 0, len(sst.Items))
	for _, item := range sst.Items {
		items = append(items, textoRichText(item))
	}
	return items, nil
}

func leerHojaXLSX(zr *zip.Reader) ([]byte, error) {
	data, err := leerArchivoZip(zr, "xl/workbook.xml")
	if err == nil {
		var wb xlsxWorkbook
		if xml.Unmarshal(data, &wb) == nil && len(wb.Sheets) > 0 {
			rid := strings.TrimSpace(wb.Sheets[0].RID)
			if rid != "" {
				if relaciones, err := leerArchivoZip(zr, "xl/_rels/workbook.xml.rels"); err == nil {
					var rels xlsxRelationships
					if xml.Unmarshal(relaciones, &rels) == nil {
						for _, rel := range rels.Items {
							if rel.ID != rid {
								continue
							}
							target := strings.TrimSpace(rel.Target)
							target = strings.TrimPrefix(target, "/")
							if !strings.HasPrefix(target, "xl/") {
								target = path.Join("xl", target)
							}
							if hoja, err := leerArchivoZip(zr, target); err == nil {
								return hoja, nil
							}
						}
					}
				}
			}
		}
	}

	if hoja, err := leerArchivoZip(zr, "xl/worksheets/sheet1.xml"); err == nil {
		return hoja, nil
	}

	for _, file := range zr.File {
		name := strings.ToLower(file.Name)
		if strings.HasPrefix(name, "xl/worksheets/") && strings.HasSuffix(name, ".xml") {
			return leerArchivoZip(zr, file.Name)
		}
	}

	return nil, fmt.Errorf("el Excel no tiene hojas")
}

func leerArchivoZip(zr *zip.Reader, nombre string) ([]byte, error) {
	for _, file := range zr.File {
		if strings.EqualFold(file.Name, nombre) {
			rc, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("no se encontró %s", nombre)
}

func valorCeldaXLSX(tipo, valor string, inline *xlsxRichText, compartidos []string) string {
	switch tipo {
	case "inlineStr":
		if inline != nil {
			return limpiarCelda(textoRichText(*inline))
		}
		return ""
	case "s":
		indice, err := strconv.Atoi(strings.TrimSpace(valor))
		if err != nil || indice < 0 || indice >= len(compartidos) {
			return ""
		}
		return limpiarCelda(compartidos[indice])
	case "str":
		return limpiarCelda(valor)
	}

	valor = strings.TrimSpace(valor)
	if strings.HasSuffix(valor, ".0") {
		valor = strings.TrimSuffix(valor, ".0")
	}
	return limpiarCelda(valor)
}

func textoRichText(item xlsxRichText) string {
	var b strings.Builder
	b.WriteString(item.Text)
	for _, run := range item.Runs {
		b.WriteString(run.Text)
	}
	return b.String()
}

func columnaDeReferencia(ref string) (int, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, false
	}

	columna := 0
	for _, char := range ref {
		if char >= 'a' && char <= 'z' {
			char -= 'a' - 'A'
		}
		if char < 'A' || char > 'Z' {
			break
		}
		columna = columna*26 + int(char-'A'+1)
	}
	if columna == 0 {
		return 0, false
	}
	return columna - 1, true
}
