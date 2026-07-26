package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	ht "html/template"
	"math"
	"strings"
	"sync"
)

//go:embed departements.geojson
var franceGeoJSON []byte

// deptPath contient la description SVG pre-calculee pour un departement.
type deptPath struct {
	Code string
	Name string
	D    string // attribut "d" du path SVG
}

// mapFeature porte un departement parse depuis le GeoJSON, ses polygones deja
// normalises en [][][][2]float64.
type mapFeature struct {
	Code  string
	Name  string
	Polys [][][][2]float64
}

// projectedMap regroupe les paths SVG d'une carte et ses dimensions (unites SVG).
type projectedMap struct {
	Paths []deptPath
	VB    string // viewBox "0 0 W H"
	W, H  float64
}

var (
	mapOnce   sync.Once
	franceMap projectedMap
	idfMap    projectedMap
	mapErr    error
)

// mapWidth est la largeur en unites SVG cible pour la carte de France.
const mapWidth = 1000.0

// idfWidth est la largeur en unites SVG cible pour l'encart Ile-de-France.
const idfWidth = 320.0

// idfCodes recense les departements d'Ile-de-France, mis en avant dans l'encart
// de zoom (la petite couronne etant illisible a l'echelle nationale).
var idfCodes = map[string]bool{
	"75": true, "77": true, "78": true, "91": true,
	"92": true, "93": true, "94": true, "95": true,
}

// buildMaps parse le GeoJSON une seule fois et pre-calcule les paths SVG de la
// carte nationale et de l'encart Ile-de-France.
func buildMaps() {
	var fc struct {
		Features []struct {
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
			Properties struct {
				Code string `json:"code"`
				Nom  string `json:"nom"`
			} `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(franceGeoJSON, &fc); err != nil {
		mapErr = fmt.Errorf("geojson invalide: %w", err)
		return
	}

	feats := make([]mapFeature, 0, len(fc.Features))
	for _, f := range fc.Features {
		polys, err := extractPolygons(f.Geometry.Type, f.Geometry.Coordinates)
		if err != nil {
			mapErr = fmt.Errorf("%s: %w", f.Properties.Code, err)
			return
		}
		feats = append(feats, mapFeature{Code: f.Properties.Code, Name: f.Properties.Nom, Polys: polys})
	}

	franceMap = projectFeatures(feats, nil, mapWidth)
	idfMap = projectFeatures(feats, idfCodes, idfWidth)
}

// projectFeatures projette les features retenus (filter nil => tous) dans leur
// propre bounding box, mise a l'echelle sur targetWidth, et renvoie les paths
// SVG ainsi que les dimensions resultantes. La projection est equirectangulaire
// avec compensation de la latitude moyenne.
func projectFeatures(feats []mapFeature, filter map[string]bool, targetWidth float64) projectedMap {
	minLon, maxLon := math.Inf(1), math.Inf(-1)
	minLat, maxLat := math.Inf(1), math.Inf(-1)
	for _, f := range feats {
		if filter != nil && !filter[f.Code] {
			continue
		}
		for _, poly := range f.Polys {
			for _, ring := range poly {
				for _, p := range ring {
					minLon = math.Min(minLon, p[0])
					maxLon = math.Max(maxLon, p[0])
					minLat = math.Min(minLat, p[1])
					maxLat = math.Max(maxLat, p[1])
				}
			}
		}
	}

	meanLatRad := (minLat + maxLat) / 2 * math.Pi / 180
	kx := math.Cos(meanLatRad)
	projW := (maxLon - minLon) * kx
	projH := maxLat - minLat
	scale := targetWidth / projW

	project := func(lon, lat float64) (float64, float64) {
		return (lon - minLon) * kx * scale, (maxLat - lat) * scale
	}

	pm := projectedMap{W: projW * scale, H: projH * scale}
	pm.VB = fmt.Sprintf("0 0 %.2f %.2f", pm.W, pm.H)

	for _, f := range feats {
		if filter != nil && !filter[f.Code] {
			continue
		}
		var b strings.Builder
		for _, poly := range f.Polys {
			for _, ring := range poly {
				if len(ring) == 0 {
					continue
				}
				for i, p := range ring {
					x, y := project(p[0], p[1])
					if i == 0 {
						fmt.Fprintf(&b, "M%.2f %.2f", x, y)
					} else {
						fmt.Fprintf(&b, "L%.2f %.2f", x, y)
					}
				}
				b.WriteString("Z")
			}
		}
		pm.Paths = append(pm.Paths, deptPath{Code: f.Code, Name: f.Name, D: b.String()})
	}
	return pm
}

// extractPolygons normalise les coordonnees Polygon/MultiPolygon en [][][][2]float64.
// Polygon:      [ ring0 [ [lon,lat], ... ], ring1, ... ]
// MultiPolygon: [ poly0 [ ring0, ... ], poly1, ... ]
func extractPolygons(geomType string, raw json.RawMessage) ([][][][2]float64, error) {
	switch geomType {
	case "Polygon":
		var rings [][][2]float64
		if err := json.Unmarshal(raw, &rings); err != nil {
			return nil, fmt.Errorf("polygon: %w", err)
		}
		return [][][][2]float64{rings}, nil
	case "MultiPolygon":
		var polys [][][][2]float64
		if err := json.Unmarshal(raw, &polys); err != nil {
			return nil, fmt.Errorf("multipolygon: %w", err)
		}
		return polys, nil
	default:
		return nil, fmt.Errorf("geometrie %q non supportee", geomType)
	}
}

// mapData renvoie la carte nationale et l'encart Ile-de-France pre-calcules.
// La construction est faite au premier appel puis mise en cache.
func mapData() (france, idf projectedMap, err error) {
	mapOnce.Do(buildMaps)
	return franceMap, idfMap, mapErr
}

// mapDeptColors regroupe les couleurs a appliquer par departement pour une echeance.
type mapDeptColors struct {
	Fill  string // couleur de remplissage (hex)
	Label string // libelle affiche au survol
}

// renderFranceMap produit une carte SVG interactive avec toggle J / J+1.
// Tous les departements sont colores selon leur niveau de vigilance Meteo
// France (J et J+1) et cernes d'un contour sombre uniforme. Les departements
// surveilles (configures pour l'envoi d'alertes) recoivent en plus, en overlay
// par-dessus les remplissages, un contour bleu lumineux (halo via filtre SVG)
// qui les fait surgir et tranche avec la palette de vigilance
// (vert/jaune/orange/rouge), tout en restant entier quel que soit l'ordre de
// peinture. Un encart zoome sur
// l'Ile-de-France, dont la petite couronne est illisible a l'echelle nationale.
func renderFranceMap(todayAll, tomorrowAll map[string]vigilanceResult, monitored map[string]bool) (ht.HTML, error) {
	france, idf, err := mapData()
	if err != nil {
		return "", err
	}

	todayColors := make(map[string]mapDeptColors, len(todayAll))
	tomorrowColors := make(map[string]mapDeptColors, len(tomorrowAll))
	for code, r := range todayAll {
		if r.Data != nil {
			todayColors[code] = mapDeptColors{Fill: r.Data.MaxColorInfo.Hex, Label: r.Data.MaxColorInfo.Label}
		}
	}
	for code, r := range tomorrowAll {
		if r.Data != nil {
			tomorrowColors[code] = mapDeptColors{Fill: r.Data.MaxColorInfo.Hex, Label: r.Data.MaxColorInfo.Label}
		}
	}

	// Tooltip natif SVG via <title>: on montre les deux echeances afin que le
	// survol reste informatif quel que soit le mode courant, et on signale les
	// departements surveilles.
	tipFor := func(p deptPath) string {
		todayStr, tomStr := "N/A", "N/A"
		if t, ok := todayColors[p.Code]; ok {
			todayStr = t.Label
		}
		if m, ok := tomorrowColors[p.Code]; ok {
			tomStr = m.Label
		}
		suffix := ""
		if monitored[p.Code] {
			suffix = " [surveille]"
		}
		return fmt.Sprintf("%s (%s) - J: %s | J+1: %s%s", p.Name, p.Code, todayStr, tomStr, suffix)
	}
	writePath := func(b *strings.Builder, idPrefix string, p deptPath) {
		fmt.Fprintf(b, `<path id="%s-%s" class="dept" d="%s"><title>%s</title></path>`,
			idPrefix, cssIDCode(p.Code), p.D, ht.HTMLEscapeString(tipFor(p)))
	}
	// writeMonitored dessine, PAR-DESSUS tous les remplissages, un contour bleu
	// lumineux (avec halo par filtre SVG) pour chaque departement surveille. Ce
	// second passage est indispensable: dessiner la bordure sur le path de base
	// la laisse partiellement recouverte par le remplissage des departements
	// voisins traces ensuite (ordre de peinture SVG). En overlay, le contour
	// reste entier et fait litteralement surgir la zone surveillee.
	writeMonitored := func(b *strings.Builder, paths []deptPath) {
		for _, p := range paths {
			if !monitored[p.Code] {
				continue
			}
			fmt.Fprintf(b, `<path class="dept-ring" d="%s"/>`, p.D)
		}
	}

	var b strings.Builder
	b.WriteString(`<div class="france-map mode-j">`)
	b.WriteString(`<div class="mapctl"><span class="mapctl-label">Vigilance affichee :</span>`)
	b.WriteString(`<label><input type="radio" name="echeance" value="j" checked onchange="setMapMode('j')"> Aujourd'hui (J)</label>`)
	b.WriteString(`<label><input type="radio" name="echeance" value="j1" onchange="setMapMode('j1')"> Demain (J+1)</label>`)
	b.WriteString(`</div>`)

	// Styles par departement (fill par mode). Le toggle J / J+1 est purement
	// CSS pour un basculement instantane. Les regles s'appliquent a la fois a
	// la carte nationale (#dept-XX) et a l'encart IDF (#idf-XX).
	b.WriteString(`<style>`)
	b.WriteString(`.france-map .dept{fill:#e2e8e5;stroke:#1d1f1e;stroke-width:0.7;stroke-linejoin:round;transition:fill .15s}`)
	b.WriteString(`.france-map .dept-ring{fill:none;stroke:#1a56db;stroke-width:2.6;stroke-linejoin:round;stroke-linecap:round;pointer-events:none;filter:url(#fm-pop)}`)
	b.WriteString(`.france-map .inset-bg{fill:#f5f9f7;stroke:#173d38;stroke-width:1.5}`)
	b.WriteString(`.france-map .inset-label{font:700 15px system-ui,sans-serif;fill:#173d38}`)
	for code, c := range todayColors {
		fmt.Fprintf(&b, `.france-map.mode-j #dept-%s{fill:%s}`, cssIDCode(code), c.Fill)
		if idfCodes[code] {
			fmt.Fprintf(&b, `.france-map.mode-j #idf-%s{fill:%s}`, cssIDCode(code), c.Fill)
		}
	}
	for code, c := range tomorrowColors {
		fmt.Fprintf(&b, `.france-map.mode-j1 #dept-%s{fill:%s}`, cssIDCode(code), c.Fill)
		if idfCodes[code] {
			fmt.Fprintf(&b, `.france-map.mode-j1 #idf-%s{fill:%s}`, cssIDCode(code), c.Fill)
		}
	}
	b.WriteString(`</style>`)

	// L'encart Ile-de-France est loge dans une marge ajoutee a gauche du
	// viewBox (au nord-ouest, dans l'Atlantique), afin de ne recouvrir aucun
	// departement de la carte nationale.
	hasInset := len(idf.Paths) > 0 && idf.W > 0
	var insetW, insetH, padLeft float64
	viewBox := france.VB
	if hasInset {
		insetW = idfWidth
		insetH = insetW * idf.H / idf.W
		padLeft = insetW + 40
		viewBox = fmt.Sprintf("%.2f 0 %.2f %.2f", -padLeft, france.W+padLeft, france.H)
	}

	fmt.Fprintf(&b, `<svg viewBox="%s" preserveAspectRatio="xMidYMid meet" role="img" aria-label="Carte de vigilance des departements de France metropolitaine">`, viewBox)
	// Filtre "pop": halo bleu lumineux double (large + serre) qui fait surgir
	// les departements surveilles au-dessus de la carte. Reference par les deux
	// svg (national + encart) via url(#fm-pop).
	b.WriteString(`<defs><filter id="fm-pop" x="-40%" y="-40%" width="180%" height="180%">` +
		`<feDropShadow dx="0" dy="0" stdDeviation="3.2" flood-color="#1a56db" flood-opacity="0.95"/>` +
		`<feDropShadow dx="0" dy="0" stdDeviation="1.4" flood-color="#0b2f8f" flood-opacity="0.9"/>` +
		`</filter></defs>`)
	for _, p := range france.Paths {
		writePath(&b, "dept", p)
	}
	// Overlay des contours surveilles, par-dessus tous les remplissages.
	writeMonitored(&b, france.Paths)

	// Encart Ile-de-France, place dans la marge gauche (nord-ouest, en mer)
	// pour zoomer la petite couronne illisible a l'echelle nationale.
	if hasInset {
		x0 := -padLeft + 20
		y0 := 40.0
		fmt.Fprintf(&b, `<rect class="inset-bg" x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="4"/>`, x0, y0, insetW, insetH)
		fmt.Fprintf(&b, `<text class="inset-label" x="%.2f" y="%.2f" text-anchor="middle">Île-de-France</text>`, x0+insetW/2, y0-7)
		fmt.Fprintf(&b, `<svg x="%.2f" y="%.2f" width="%.2f" height="%.2f" viewBox="%s" preserveAspectRatio="xMidYMid meet">`, x0, y0, insetW, insetH, idf.VB)
		for _, p := range idf.Paths {
			writePath(&b, "idf", p)
		}
		writeMonitored(&b, idf.Paths)
		b.WriteString(`</svg>`)
	}

	b.WriteString(`</svg>`)

	// Legende
	b.WriteString(`<div class="maplegend">`)
	for _, code := range []int{1, 2, 3, 4} {
		c := colorMap[code]
		fmt.Fprintf(&b, `<span class="lg"><span class="sw" style="background:%s"></span>%s %s</span>`, c.Hex, c.Emoji, c.Label)
	}
	b.WriteString(`<span class="lg"><span class="sw" style="background:#e2e8e5;border:1px solid #94a8a3"></span>Donnee indisponible</span>`)
	b.WriteString(`<span class="lg"><span class="sw" style="background:#e2e8e5;border:2px solid #1a56db;box-shadow:0 0 5px 1px #1a56db"></span>Surveille</span>`)
	b.WriteString(`</div>`)

	b.WriteString(`</div>`)
	return ht.HTML(b.String()), nil
}

// cssIDCode transforme un code de departement en identifiant HTML sur (Corse: 2A/2B).
func cssIDCode(code string) string {
	// Les codes sont deja alphanumeriques simples (chiffres + 2A/2B).
	return code
}
