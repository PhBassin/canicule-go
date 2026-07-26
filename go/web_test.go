package main

import (
	"bytes"
	"html/template"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestAdminPageRendersDepartmentControls(t *testing.T) {
	tmpl, err := template.New("admin").Funcs(template.FuncMap{
		"colors": func() []string { return []string{"jaune", "orange", "rouge"} },
		"join":   func(items []string, separator string) string { return "" },
	}).Parse(adminPage)
	if err != nil {
		t.Fatal(err)
	}
	var page bytes.Buffer
	err = tmpl.Execute(&page, pageData{Config: &Config{
		SMTP:           SMTPConfig{Host: "smtp.example.test", Sender: "alerts@example.test"},
		GlobalSettings: GlobalSettings{MinAlertColor: "jaune", StateFilePath: "state.json", TemplateDir: "templates"},
		Regions:        []Region{{DepartmentCode: "75", Name: "Paris"}},
	}, Departments: departments})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(page.Bytes(), []byte("Ajouter un departement")) {
		t.Fatal("department add control is missing from rendered page")
	}
}

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

func TestLastScheduledRefresh(t *testing.T) {
	loc := time.UTC
	day := func(h, m int) time.Time { return time.Date(2026, 7, 26, h, m, 0, 0, loc) }
	interval := 12 * time.Hour // creneaux: 06:00 et 18:00

	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"apres le creneau du matin", day(10, 0), day(6, 0)},
		{"pile sur un creneau", day(18, 0), day(18, 0)},
		{"apres le creneau du soir", day(18, 30), day(18, 0)},
		{"avant le premier creneau du jour", day(5, 0), time.Date(2026, 7, 25, 18, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := lastScheduledRefresh(c.now, 6, 0, interval)
			if !got.Equal(c.want) {
				t.Fatalf("lastScheduledRefresh(%v) = %v, want %v", c.now, got, c.want)
			}
		})
	}
}

func TestRefreshScheduleDefaults(t *testing.T) {
	h, m, interval := refreshSchedule(GlobalSettings{})
	if h != 6 || m != 0 || interval != 12*time.Hour {
		t.Fatalf("defauts = %02d:%02d/%v, want 06:00/12h", h, m, interval)
	}
	h, m, interval = refreshSchedule(GlobalSettings{RefreshStart: "08:30", RefreshIntervalHours: 6})
	if h != 8 || m != 30 || interval != 6*time.Hour {
		t.Fatalf("configures = %02d:%02d/%v, want 08:30/6h", h, m, interval)
	}
}
