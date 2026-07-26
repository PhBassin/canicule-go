package main

import (
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestUpdateConfigFromFormBuildsDepartments(t *testing.T) {
	cfg := &Config{Regions: []Region{{DepartmentCode: "75"}}}
	form := url.Values{
		"region_count":        {"2"},
		"region_0_department": {"75"},
		"region_0_color":      {"jaune"},
		"region_0_recipients": {"paris@example.test, astreinte@example.test"},
		"region_1_department": {"13"},
		"region_1_color":      {"orange"},
		"region_1_recipients": {"marseille@example.test"},
	}
	req := httptest.NewRequest("POST", "/", nil)
	req.Form = form

	updateConfigFromForm(cfg, req)

	if len(cfg.Regions) != 2 {
		t.Fatalf("got %d regions, want 2", len(cfg.Regions))
	}
	if cfg.Regions[0].Name != "Paris" || cfg.Regions[0].ID != "75" {
		t.Fatalf("unexpected Paris region: %#v", cfg.Regions[0])
	}
	if cfg.Regions[1].Name != "Bouches-du-Rhone" || cfg.Regions[1].MinAlertColor != "orange" {
		t.Fatalf("unexpected Marseille region: %#v", cfg.Regions[1])
	}
}

func TestValidateConfigRejectsDuplicateDepartment(t *testing.T) {
	cfg := &Config{
		SMTP:           SMTPConfig{Host: "smtp.example.test", Sender: "alerts@example.test"},
		GlobalSettings: GlobalSettings{MinAlertColor: "jaune", StateFilePath: "state.json", TemplateDir: "templates"},
		Regions: []Region{
			{Name: "Paris", DepartmentCode: "75"},
			{Name: "Paris", DepartmentCode: "75"},
		},
	}

	if err := validateConfig(cfg); err == nil {
		t.Fatal("validateConfig accepted duplicate departments")
	}
}

func TestRenderSubject(t *testing.T) {
	subject, err := renderSubject("[{{.ColorLabelUpper}}] {{.RegionName}}", templateData{ColorLabelUpper: "ORANGE", RegionName: "Paris"})
	if err != nil {
		t.Fatal(err)
	}
	if subject != "[ORANGE] Paris" {
		t.Fatalf("got %q", subject)
	}
}

func TestRenderSubjectRemovesLineBreaks(t *testing.T) {
	subject, err := renderSubject("Alert\n{{.RegionName}}", templateData{RegionName: "Paris"})
	if err != nil {
		t.Fatal(err)
	}
	if subject != "Alert Paris" {
		t.Fatalf("got %q", subject)
	}
}
