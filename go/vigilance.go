package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	mfPublicToken = "__Wj7dVSTjV9YGu1guveLyDq0g7S7TfTjaHBTPTpO0kj8__"
	mfAPIURL      = "https://webservice.meteofrance.com/v3/warning/currentphenomenons"

	// EcheanceToday demande la vigilance du jour meme (J).
	EcheanceToday = "J"
	// EcheanceTomorrow demande la prevision de vigilance pour le lendemain (J+1).
	EcheanceTomorrow = "J1"
)

// echeanceLabel renvoie une etiquette humaine pour l'echeance passee.
func echeanceLabel(echeance string) string {
	switch echeance {
	case EcheanceTomorrow:
		return "demain (J+1)"
	case EcheanceToday, "":
		return "aujourd'hui (J)"
	default:
		return echeance
	}
}

type ColorInfo struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Code  int    `json:"code"`
	Emoji string `json:"emoji"`
	Hex   string `json:"hex"`
}

type Phenomenon struct {
	ID       int    `json:"phenomenon_id"`
	Name     string `json:"phenomenon_name"`
	ColorID  int    `json:"phenomenon_max_color_id"`
	Color    ColorInfo
}

type VigilanceData struct {
	DepartmentCode string
	Echeance       string
	MaxColorCode   int
	MaxColorInfo   ColorInfo
	UpdateTime     int64
	EndValidity    int64
	Phenomena      []Phenomenon
}

var phenomenaMap = map[int]string{
	1: "Vent violent",
	2: "Pluie-inondation",
	3: "Orages",
	4: "Crues",
	5: "Neige-verglas",
	6: "Canicule",
	7: "Grand froid",
	8: "Avalanches",
	9: "Vagues-submersion",
}

var colorMap = map[int]ColorInfo{
	1: {Name: "vert", Label: "Vert (Normal)", Code: 1, Emoji: "🟢", Hex: "#31aa35"},
	2: {Name: "jaune", Label: "Jaune (Soyez attentif)", Code: 2, Emoji: "🟡", Hex: "#f5c518"},
	3: {Name: "orange", Label: "Orange (Soyez tres vigilant)", Code: 3, Emoji: "🟠", Hex: "#ff8c00"},
	4: {Name: "rouge", Label: "Rouge (Vigilance absolue)", Code: 4, Emoji: "🔴", Hex: "#d9534f"},
}

type mfResponse struct {
	PhenomenonsMaxColors []struct {
		PhenomenonID         string `json:"phenomenon_id"`
		PhenomenonMaxColorID int    `json:"phenomenon_max_color_id"`
	} `json:"phenomenons_max_colors"`
	UpdateTime      int64 `json:"update_time"`
	EndValidityTime int64 `json:"end_validity_time"`
}

func fetchVigilance(deptCode, echeance string) (*VigilanceData, error) {
	if echeance == "" {
		echeance = EcheanceToday
	}
	url := fmt.Sprintf("%s?token=%s&domain=%s&echeance=%s", mfAPIURL, mfPublicToken, deptCode, echeance)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("echec requete HTTP: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lecture reponse: %w", err)
	}

	var data mfResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	maxColor := 1
	var phenomena []Phenomenon
	for _, p := range data.PhenomenonsMaxColors {
		cID := p.PhenomenonMaxColorID
		if cID > maxColor {
			maxColor = cID
		}
		pID, _ := strconv.Atoi(p.PhenomenonID)
		ci, ok := colorMap[cID]
		if !ok {
			ci = colorMap[1]
		}
		phenomena = append(phenomena, Phenomenon{
			ID:      pID,
			Name:    phenomenaName(pID),
			ColorID: cID,
			Color:   ci,
		})
	}

	ci, ok := colorMap[maxColor]
	if !ok {
		ci = colorMap[1]
	}

	return &VigilanceData{
		DepartmentCode: deptCode,
		Echeance:       echeance,
		MaxColorCode:   maxColor,
		MaxColorInfo:   ci,
		UpdateTime:     data.UpdateTime,
		EndValidity:    data.EndValidityTime,
		Phenomena:      phenomena,
	}, nil
}

func phenomenaName(id int) string {
	if name, ok := phenomenaMap[id]; ok {
		return name
	}
	return fmt.Sprintf("Phenomene #%d", id)
}

// vigilanceResult porte le resultat d'une recuperation de vigilance pour un
// departement: soit les donnees, soit l'erreur rencontree.
type vigilanceResult struct {
	Data *VigilanceData
	Err  error
}

// maxVigilanceConcurrency borne le nombre d'appels HTTP simultanes vers l'API
// Meteo-France lors de la recuperation de tous les departements, afin de ne pas
// saturer le service (ni nos sockets sortantes).
const maxVigilanceConcurrency = 12

// fetchAllVigilance recupere la vigilance de tous les departements
// metropolitains pour une echeance donnee, en parallele et avec une
// concurrence bornee. La cle de la map retournee est le code du departement.
func fetchAllVigilance(echeance string) map[string]vigilanceResult {
	results := make(map[string]vigilanceResult, len(departments))
	sem := make(chan struct{}, maxVigilanceConcurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, d := range departments {
		wg.Add(1)
		sem <- struct{}{}
		go func(code string) {
			defer wg.Done()
			defer func() { <-sem }()
			v, err := fetchVigilance(code, echeance)
			mu.Lock()
			results[code] = vigilanceResult{Data: v, Err: err}
			mu.Unlock()
		}(d.Code)
	}
	wg.Wait()
	return results
}


