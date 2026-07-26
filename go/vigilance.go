package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	mfPublicToken = "__Wj7dVSTjV9YGu1guveLyDq0g7S7TfTjaHBTPTpO0kj8__"
	mfAPIURL      = "https://webservice.meteofrance.com/v3/warning/currentphenomenons"
)

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

func fetchVigilance(deptCode string) (*VigilanceData, error) {
	url := fmt.Sprintf("%s?token=%s&domain=%s", mfAPIURL, mfPublicToken, deptCode)

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


