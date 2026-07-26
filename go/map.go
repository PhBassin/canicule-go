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

var (
	mapOnce     sync.Once
	franceMap   []deptPath
	franceVB    string // viewBox du SVG France
	franceMapW  float64
	franceMapH  float64
	franceMapEr error
)

// mapWidth est la largeur en unites SVG cible pour la carte de France.
const mapWidth = 1000.0

// buildFranceMap parse le GeoJSON une seule fois et pre-calcule les paths SVG.
func buildFranceMap() {
	var fc struct {
		Features []struct {
			Type     string `json:"type"`
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
		franceMapEr = fmt.Errorf("geojson invalide: %w", err)
		return
	}

	// Premiere passe: bbox
	minLon, maxLon := math.Inf(1), math.Inf(-1)
	minLat, maxLat := math.Inf(1), math.Inf(-1)
	for _, f := range fc.Features {
		polys, err := extractPolygons(f.Geometry.Type, f.Geometry.Coordinates)
		if err != nil {
			franceMapEr = fmt.Errorf("%s: %w", f.Properties.Code, err)
			return
		}
		for _, poly := range polys {
			for _, ring := range poly {
				for _, p := range ring {
					lon, lat := p[0], p[1]
					if lon < minLon {
						minLon = lon
					}
					if lon > maxLon {
						maxLon = lon
					}
					if lat < minLat {
						minLat = lat
					}
					if lat > maxLat {
						maxLat = lat
					}
				}
			}
		}
	}

	// Projection equirectangulaire avec compensation de la latitude moyenne
	meanLatRad := (minLat + maxLat) / 2 * math.Pi / 180
	kx := math.Cos(meanLatRad)
	// Largeur/hauteur en unites projetees
	projW := (maxLon - minLon) * kx
	projH := maxLat - minLat
	scale := mapWidth / projW

	project := func(lon, lat float64) (float64, float64) {
		x := (lon - minLon) * kx * scale
		y := (maxLat - lat) * scale
		return x, y
	}

	franceMapW = projW * scale
	franceMapH = projH * scale
	franceVB = fmt.Sprintf("0 0 %.2f %.2f", franceMapW, franceMapH)

	// Deuxieme passe: paths SVG
	var out []deptPath
	for _, f := range fc.Features {
		polys, err := extractPolygons(f.Geometry.Type, f.Geometry.Coordinates)
		if err != nil {
			franceMapEr = fmt.Errorf("%s: %w", f.Properties.Code, err)
			return
		}
		var b strings.Builder
		for _, poly := range polys {
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
		out = append(out, deptPath{Code: f.Properties.Code, Name: f.Properties.Nom, D: b.String()})
	}
	franceMap = out
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

// franceMapData renvoie les paths pre-calcules et le viewBox associe.
// La construction est faite au premier appel puis mise en cache.
func franceMapData() ([]deptPath, string, error) {
	mapOnce.Do(buildFranceMap)
	return franceMap, franceVB, franceMapEr
}

// mapDeptColors regroupe les couleurs a appliquer par departement pour une echeance.
type mapDeptColors struct {
	Fill  string // couleur de remplissage (hex)
	Label string // libelle affiche au survol
}

// renderFranceMap produit une carte SVG interactive avec toggle J / J+1.
// Tous les departements sont colores selon leur niveau de vigilance Meteo
// France (J et J+1). Les departements surveilles (configures pour l'envoi
// d'alertes) sont en plus entoures d'une bordure epaisse pour les mettre en
// evidence. Les departements dont la donnee n'a pu etre recuperee restent en
// gris.
func renderFranceMap(todayAll, tomorrowAll map[string]vigilanceResult, monitored map[string]bool) (ht.HTML, error) {
	paths, viewBox, err := franceMapData()
	if err != nil {
		return "", err
	}

	todayColors := make(map[string]mapDeptColors, len(todayAll))
	tomorrowColors := make(map[string]mapDeptColors, len(tomorrowAll))
	for code, r := range todayAll {
		if r.Data != nil {
			todayColors[code] = mapDeptColors{
				Fill:  r.Data.MaxColorInfo.Hex,
				Label: r.Data.MaxColorInfo.Label,
			}
		}
	}
	for code, r := range tomorrowAll {
		if r.Data != nil {
			tomorrowColors[code] = mapDeptColors{
				Fill:  r.Data.MaxColorInfo.Hex,
				Label: r.Data.MaxColorInfo.Label,
			}
		}
	}

	var b strings.Builder
	b.WriteString(`<div class="france-map mode-j">`)
	b.WriteString(`<div class="mapctl"><span class="mapctl-label">Vigilance affichee :</span>`)
	b.WriteString(`<label><input type="radio" name="echeance" value="j" checked onchange="setMapMode('j')"> Aujourd'hui (J)</label>`)
	b.WriteString(`<label><input type="radio" name="echeance" value="j1" onchange="setMapMode('j1')"> Demain (J+1)</label>`)
	b.WriteString(`</div>`)

	// Styles par departement (fill par mode). On stocke les deux couleurs en
	// data-attributes, mais on applique via des regles CSS pour un toggle
	// instantane et sans reflow des attributs SVG.
	b.WriteString(`<style>`)
	b.WriteString(`.france-map .dept{fill:#e2e8e5;stroke:#94a8a3;stroke-width:0.5;transition:fill .15s}`)
	b.WriteString(`.france-map .dept.monitored{stroke:#173d38;stroke-width:2}`)
	for code, c := range todayColors {
		fmt.Fprintf(&b, `.france-map.mode-j #dept-%s{fill:%s}`, cssIDCode(code), c.Fill)
	}
	for code, c := range tomorrowColors {
		fmt.Fprintf(&b, `.france-map.mode-j1 #dept-%s{fill:%s}`, cssIDCode(code), c.Fill)
	}
	b.WriteString(`</style>`)

	fmt.Fprintf(&b, `<svg viewBox="%s" preserveAspectRatio="xMidYMid meet" role="img" aria-label="Carte de vigilance des departements de France metropolitaine">`, viewBox)
	for _, p := range paths {
		class := "dept"
		if monitored[p.Code] {
			class += " monitored"
		}
		// Tooltip natif SVG via <title>. On montre les deux echeances afin
		// que le survol reste informatif quel que soit le mode d'affichage
		// courant, et on signale les departements surveilles.
		todayStr := "N/A"
		tomStr := "N/A"
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
		tip := fmt.Sprintf("%s (%s) - J: %s | J+1: %s%s", p.Name, p.Code, todayStr, tomStr, suffix)
		fmt.Fprintf(&b,
			`<path id="dept-%s" class="%s" d="%s"><title>%s</title></path>`,
			cssIDCode(p.Code), class, p.D, ht.HTMLEscapeString(tip))
	}
	b.WriteString(`</svg>`)

	// Legende
	b.WriteString(`<div class="maplegend">`)
	for _, code := range []int{1, 2, 3, 4} {
		c := colorMap[code]
		fmt.Fprintf(&b, `<span class="lg"><span class="sw" style="background:%s"></span>%s %s</span>`, c.Hex, c.Emoji, c.Label)
	}
	b.WriteString(`<span class="lg"><span class="sw" style="background:#e2e8e5;border:1px solid #94a8a3"></span>Donnee indisponible</span>`)
	b.WriteString(`<span class="lg"><span class="sw" style="background:#fff;border:2px solid #173d38"></span>Surveille</span>`)
	b.WriteString(`</div>`)

	b.WriteString(`</div>`)
	return ht.HTML(b.String()), nil
}

// cssIDCode transforme un code de departement en identifiant HTML sur (Corse: 2A/2B).
func cssIDCode(code string) string {
	// Les codes sont deja alphanumeriques simples (chiffres + 2A/2B).
	return code
}
