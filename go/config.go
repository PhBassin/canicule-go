package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type SMTPConfig struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	UseTLS     bool   `json:"use_tls"`
	UseSSL     bool   `json:"use_ssl"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Sender     string `json:"sender_email"`
	SenderName string `json:"sender_name"`
	DryRun     bool   `json:"dry_run"`
}

type GlobalSettings struct {
	MinAlertColor      string `json:"min_alert_color"`
	OnlyNotifyOnChange bool   `json:"only_notify_on_change"`
	StateFilePath      string `json:"state_file_path"`
	TemplateDir        string `json:"template_dir"`
	SubjectTemplate    string `json:"subject_template"`
	// RefreshStart est l'heure de depart (format "HH:MM") du rafraichissement
	// de la carte de vigilance. Vide => valeur par defaut (06:00).
	RefreshStart string `json:"refresh_start"`
	// RefreshIntervalHours est l'intervalle, en heures, entre deux
	// rafraichissements de la carte. 0 => valeur par defaut (12 h).
	RefreshIntervalHours int `json:"refresh_interval_hours"`
}

type Region struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	DepartmentCodes []string `json:"department_codes"`
	// DepartmentCode is retained only to read configurations written before
	// perimeters could contain more than one department.
	DepartmentCode string   `json:"department_code,omitempty"`
	MinAlertColor  string   `json:"min_alert_color"`
	DistList       []string `json:"distribution_list"`
}

type Config struct {
	SMTP           SMTPConfig      `json:"smtp"`
	GlobalSettings GlobalSettings  `json:"global_settings"`
	Logging        json.RawMessage `json:"logging,omitempty"`
	Regions        []Region        `json:"regions"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fichier de configuration non trouve: %s", path)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("fichier JSON invalide (%s): %w", path, err)
	}
	for i := range cfg.Regions {
		if len(cfg.Regions[i].DepartmentCodes) == 0 && cfg.Regions[i].DepartmentCode != "" {
			cfg.Regions[i].DepartmentCodes = []string{cfg.Regions[i].DepartmentCode}
		}
		cfg.Regions[i].DepartmentCode = ""
	}
	return &cfg, nil
}

func saveConfig(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encodage configuration: %w", err)
	}
	return writeFileAtomically(path, data, 0600)
}

func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".canicule-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
