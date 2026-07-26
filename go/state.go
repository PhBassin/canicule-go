package main

import (
	"encoding/json"
	"os"
	"time"
)

type DeptState struct {
	MaxColorCode     int    `json:"max_color_code"`
	LastNotification string `json:"last_notification,omitempty"`
	UpdateTime       int64  `json:"update_time,omitempty"`
	LastCheck        string `json:"last_check,omitempty"`
}

func loadState(path string) (map[string]DeptState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state map[string]DeptState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return state, nil
}

func saveState(path string, state map[string]DeptState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func nowISO() string {
	return time.Now().Format(time.RFC3339)
}

func formatTimestamp(ts int64) string {
	if ts == 0 {
		return "N/A"
	}
	return time.Unix(ts, 0).Format("02/01/2006 a 15:04:05")
}
