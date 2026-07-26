package main

import (
	"encoding/json"
	"fmt"
	"os"
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
}

type Region struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	DepartmentCode string   `json:"department_code"`
	MinAlertColor  string   `json:"min_alert_color"`
	DistList       []string `json:"distribution_list"`
}

type Config struct {
	SMTP           SMTPConfig     `json:"smtp"`
	GlobalSettings GlobalSettings `json:"global_settings"`
	Regions        []Region       `json:"regions"`
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
	return &cfg, nil
}
