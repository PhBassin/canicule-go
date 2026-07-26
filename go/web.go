package main

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type webServer struct {
	configPath, username, password string
	mu                             sync.Mutex

	refreshMu   sync.Mutex
	vigCacheMu  sync.Mutex
	vigToday    map[string]vigilanceResult
	vigTomorrow map[string]vigilanceResult
	vigCachedAt time.Time
}
type pageData struct {
	Config                            *Config
	Departments                       []department
	Templates                         []string
	TemplatePage                      bool
	Selected, Content, Message, Error string
}

// statusEntry regroupe la vigilance J et J+1 pour un departement configure.
type statusEntry struct {
	Region      Region
	Today       *VigilanceData
	TodayErr    string
	Tomorrow    *VigilanceData
	TomorrowErr string
}

type statusPageData struct {
	Entries []statusEntry
	Error   string
	NowStr  string
	MapHTML template.HTML
}

func runWebServer(address, configPath string) error {
	username, password := os.Getenv("CANICULE_WEB_USER"), os.Getenv("CANICULE_WEB_PASSWORD")
	if username == "" || password == "" {
		return fmt.Errorf("CANICULE_WEB_USER et CANICULE_WEB_PASSWORD doivent etre definis pour lancer l'interface web")
	}
	s := &webServer{configPath: configPath, username: username, password: password}
	go s.runStatusRefresher()
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleStatus)
	mux.HandleFunc("/config", s.withAuth(s.handleConfig))
	mux.HandleFunc("/templates", s.withAuth(s.handleTemplates))
	fmt.Printf("Interface web disponible sur http://%s\n", address)
	return (&http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}).ListenAndServe()
}

func (s *webServer) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != s.username || password != s.password {
			w.Header().Set("WWW-Authenticate", `Basic realm="Canicule"`)
			http.Error(w, "Authentification requise", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
func parsePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *webServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Methode non autorisee", http.StatusMethodNotAllowed)
		return
	}
	if !parsePost(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		s.render(w, pageData{Error: err.Error()})
		return
	}
	data := pageData{Config: cfg, Departments: departments}
	if r.Method == http.MethodPost {
		updateConfigFromForm(cfg, r)
		if err := validateConfig(cfg); err != nil {
			data.Error = err.Error()
		} else if err := saveConfig(s.configPath, cfg); err != nil {
			data.Error = "Sauvegarde impossible: " + err.Error()
		} else {
			data.Message = "Configuration enregistree."
		}
	}
	s.render(w, data)
}

func (s *webServer) handleTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Methode non autorisee", http.StatusMethodNotAllowed)
		return
	}
	if !parsePost(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		s.render(w, pageData{Error: err.Error()})
		return
	}
	dir := cfg.GlobalSettings.TemplateDir
	if dir == "" {
		dir = "templates"
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(filepath.Dir(s.configPath), dir)
	}
	name := r.FormValue("name")
	if name == "" {
		name = "default.html"
	}
	data := pageData{Config: cfg, TemplatePage: true, Selected: name, Templates: listTemplates(dir)}
	if r.Method == http.MethodPost {
		content := r.FormValue("content")
		if !validTemplateName(name) {
			data.Error = "Nom de modele invalide."
		} else if err := validateEmailTemplate(name, content); err != nil {
			data.Error = "Modele Go invalide: " + err.Error()
		} else if err := writeFileAtomically(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			data.Error = "Sauvegarde impossible: " + err.Error()
		} else {
			data.Message, data.Content = "Modele enregistre.", content
		}
	}
	if data.Content == "" && validTemplateName(name) {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil && !os.IsNotExist(err) {
			data.Error = "Lecture impossible: " + err.Error()
		} else {
			data.Content = string(content)
		}
	}
	s.render(w, data)
}

// defaultRefreshStartHour / defaultRefreshInterval definissent la planification
// par defaut du rafraichissement de la carte de la page d'accueil: un depart a 06:00 et un
// intervalle de 12 h (soit deux rafraichissements par jour). Chaque
// rafraichissement declenche ~192 appels a l'API Meteo-France (96 departements
// x 2 echeances), car il n'existe pas d'endpoint national avec ce token.
const (
	defaultRefreshStartHour   = 6
	defaultRefreshStartMinute = 0
	defaultRefreshInterval    = 12 * time.Hour
)

// refreshSchedule extrait l'heure de depart et l'intervalle de rafraichissement
// depuis la configuration, en appliquant les valeurs par defaut lorsque les
// champs sont vides ou invalides.
func refreshSchedule(g GlobalSettings) (hour, minute int, interval time.Duration) {
	hour, minute = defaultRefreshStartHour, defaultRefreshStartMinute
	if h, m, ok := parseHHMM(g.RefreshStart); ok {
		hour, minute = h, m
	}
	interval = defaultRefreshInterval
	if g.RefreshIntervalHours > 0 {
		interval = time.Duration(g.RefreshIntervalHours) * time.Hour
	}
	return hour, minute, interval
}

// parseHHMM lit une heure au format "HH:MM". ok vaut false si le format est
// invalide ou hors bornes.
func parseHHMM(s string) (hour, minute int, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false
	}
	if n, err := fmt.Sscanf(s, "%d:%d", &hour, &minute); err != nil || n != 2 {
		return 0, 0, false
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// lastScheduledRefresh renvoie l'instant de rafraichissement planifie le plus
// recent anterieur ou egal a now, en partant de l'heure de depart du jour et en
// se decalant par pas d'intervalle (dans les deux sens si besoin).
func lastScheduledRefresh(now time.Time, hour, minute int, interval time.Duration) time.Time {
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	anchor := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	for anchor.After(now) {
		anchor = anchor.Add(-interval)
	}
	for !anchor.Add(interval).After(now) {
		anchor = anchor.Add(interval)
	}
	return anchor
}

func (s *webServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Methode non autorisee", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	cfg, err := loadConfig(s.configPath)
	s.mu.Unlock()
	if err != nil {
		s.renderStatus(w, statusPageData{Error: err.Error(), NowStr: nowISO()})
		return
	}

	// Seules les donnees de vigilance (couteuses: ~192 appels API) sont mises
	// en cache selon la planification. La carte et les fiches sont en revanche
	// reconstruites a CHAQUE visite a partir de la configuration courante, afin
	// que les departements surveilles affiches refletent immediatement toute
	// modification de la config, sans attendre le prochain rafraichissement.
	today, tomorrow := s.ensureVigilance(cfg)
	s.renderStatus(w, buildStatusData(cfg, today, tomorrow))
}

// vigilanceStale indique si le cache de vigilance doit etre reconstruit: soit un
// creneau planifie est passe depuis le dernier rafraichissement, soit le cache a
// depasse la duree de l'intervalle (filet de securite lors d'une visite).
func (s *webServer) vigilanceStale(cachedAt, lastRefresh time.Time, interval time.Duration) bool {
	return cachedAt.IsZero() || cachedAt.Before(lastRefresh) || time.Since(cachedAt) >= interval
}

// ensureVigilance renvoie les donnees de vigilance J et J+1 de tous les
// departements, en rafraichissant le cache s'il est perime. Les
// rafraichissements sont serialises (refreshMu) pour eviter deux tempetes de
// requetes simultanees entre le planificateur et une visite.
func (s *webServer) ensureVigilance(cfg *Config) (today, tomorrow map[string]vigilanceResult) {
	hour, minute, interval := refreshSchedule(cfg.GlobalSettings)
	lastRefresh := lastScheduledRefresh(time.Now(), hour, minute, interval)

	s.vigCacheMu.Lock()
	today, tomorrow = s.vigToday, s.vigTomorrow
	cachedAt := s.vigCachedAt
	s.vigCacheMu.Unlock()
	if !s.vigilanceStale(cachedAt, lastRefresh, interval) {
		return today, tomorrow
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	// Un autre rafraichissement a pu se terminer pendant l'attente du verrou.
	s.vigCacheMu.Lock()
	today, tomorrow = s.vigToday, s.vigTomorrow
	cachedAt = s.vigCachedAt
	s.vigCacheMu.Unlock()
	if !s.vigilanceStale(cachedAt, lastRefresh, interval) {
		return today, tomorrow
	}
	return s.refreshVigilance()
}

// refreshVigilance recupere la vigilance de tous les departements metropolitains
// (J et J+1, en parallele), met a jour le cache et renvoie les donnees.
// L'appelant doit detenir refreshMu.
func (s *webServer) refreshVigilance() (today, tomorrow map[string]vigilanceResult) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); today = fetchAllVigilance(EcheanceToday) }()
	go func() { defer wg.Done(); tomorrow = fetchAllVigilance(EcheanceTomorrow) }()
	wg.Wait()

	s.vigCacheMu.Lock()
	s.vigToday, s.vigTomorrow, s.vigCachedAt = today, tomorrow, time.Now()
	s.vigCacheMu.Unlock()
	return today, tomorrow
}

// buildStatusData assemble la page d'accueil (carte) a partir de la configuration courante
// et des donnees de vigilance en cache. La carte affiche l'ensemble des
// vigilances; les departements surveilles (config courante) sont mis en
// evidence et les fiches detaillees ne concernent qu'eux.
func buildStatusData(cfg *Config, todayAll, tomorrowAll map[string]vigilanceResult) statusPageData {
	monitored := make(map[string]bool)
	var entries []statusEntry
	for _, region := range cfg.Regions {
		for _, code := range region.DepartmentCodes {
			entry := statusEntry{Region: Region{ID: region.ID, Name: region.Name, DepartmentCode: code, DepartmentCodes: []string{code}}}
			if department, ok := departmentByCode(code); ok {
				entry.Region.Name = fmt.Sprintf("%s - %s (%s)", region.Name, department.Name, code)
			}
			monitored[code] = true
			if r, ok := todayAll[code]; ok {
				if r.Err != nil {
					entry.TodayErr = r.Err.Error()
				} else {
					entry.Today = r.Data
				}
			} else {
				entry.TodayErr = "departement inconnu"
			}
			if r, ok := tomorrowAll[code]; ok {
				if r.Err != nil {
					entry.TomorrowErr = r.Err.Error()
				} else {
					entry.Tomorrow = r.Data
				}
			} else {
				entry.TomorrowErr = "departement inconnu"
			}
			entries = append(entries, entry)
		}
	}

	data := statusPageData{Entries: entries, NowStr: nowISO()}
	if mapHTML, err := renderFranceMap(todayAll, tomorrowAll, monitored); err != nil {
		data.Error = "Carte indisponible: " + err.Error()
	} else {
		data.MapHTML = mapHTML
	}
	return data
}

// runStatusRefresher rafraichit periodiquement le cache de vigilance en tache de
// fond, selon l'heure de depart et l'intervalle configures, meme sans visite.
// Un rafraichissement initial rechauffe le cache au demarrage.
func (s *webServer) runStatusRefresher() {
	refreshNow := func() {
		s.refreshMu.Lock()
		s.refreshVigilance()
		s.refreshMu.Unlock()
	}

	refreshNow()
	for {
		s.mu.Lock()
		cfg, err := loadConfig(s.configPath)
		s.mu.Unlock()
		hour, minute, interval := defaultRefreshStartHour, defaultRefreshStartMinute, defaultRefreshInterval
		if err == nil {
			hour, minute, interval = refreshSchedule(cfg.GlobalSettings)
		}
		now := time.Now()
		next := lastScheduledRefresh(now, hour, minute, interval).Add(interval)
		time.Sleep(time.Until(next))
		refreshNow()
	}
}

func (s *webServer) render(w http.ResponseWriter, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, err := template.New("admin").Funcs(template.FuncMap{"colors": func() []string { return []string{"jaune", "orange", "rouge"} }, "join": strings.Join}).Parse(perimeterAdminPage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *webServer) renderStatus(w http.ResponseWriter, data statusPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl, err := template.New("status").Parse(statusPage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func updateConfigFromForm(cfg *Config, r *http.Request) {
	cfg.SMTP = SMTPConfig{Host: r.FormValue("smtp_host"), Port: formInt(r.FormValue("smtp_port"), 587), UseTLS: r.FormValue("smtp_tls") != "", UseSSL: r.FormValue("smtp_ssl") != "", Username: r.FormValue("smtp_username"), Password: r.FormValue("smtp_password"), Sender: r.FormValue("smtp_sender"), SenderName: r.FormValue("smtp_sender_name"), DryRun: r.FormValue("smtp_dry_run") != ""}
	cfg.GlobalSettings.MinAlertColor = r.FormValue("min_alert_color")
	cfg.GlobalSettings.OnlyNotifyOnChange = r.FormValue("only_notify") != ""
	cfg.GlobalSettings.StateFilePath = r.FormValue("state_file")
	cfg.GlobalSettings.TemplateDir = r.FormValue("template_dir")
	cfg.GlobalSettings.SubjectTemplate = r.FormValue("subject_template")
	cfg.GlobalSettings.RefreshStart = strings.TrimSpace(r.FormValue("refresh_start"))
	cfg.GlobalSettings.RefreshIntervalHours = formInt(r.FormValue("refresh_interval_hours"), 12)
	count := formCount(r.FormValue("region_count"))
	cfg.Regions = nil
	for i := 0; i < count; i++ {
		prefix := fmt.Sprintf("region_%d_", i)
		if r.FormValue(prefix+"remove") != "" {
			continue
		}
		codes := recipients(r.FormValue(prefix + "departments"))
		if len(codes) == 0 {
			continue
		}
		name := strings.TrimSpace(r.FormValue(prefix + "name"))
		if name == "" {
			name = codes[0]
		}
		cfg.Regions = append(cfg.Regions, Region{ID: strings.ToLower(strings.ReplaceAll(name, " ", "-")), Name: name, DepartmentCodes: codes, MinAlertColor: r.FormValue(prefix + "color"), DistList: recipients(r.FormValue(prefix + "recipients"))})
	}
}
func recipients(value string) []string {
	items := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' || r == ';' })
	for i := range items {
		items[i] = strings.TrimSpace(items[i])
	}
	return items
}
func formInt(value string, fallback int) int {
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n < 1 || n > 65535 {
		return fallback
	}
	return n
}

func formCount(value string) int {
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n < 0 || n > len(departments) {
		return 0
	}
	return n
}
func validateConfig(cfg *Config) error {
	if cfg.SMTP.Host == "" || cfg.SMTP.Sender == "" {
		return fmt.Errorf("L'hote SMTP et l'adresse d'expedition sont obligatoires.")
	}
	if colorNameToCode(cfg.GlobalSettings.MinAlertColor) == 0 {
		return fmt.Errorf("Couleur minimale invalide.")
	}
	if cfg.GlobalSettings.StateFilePath == "" || cfg.GlobalSettings.TemplateDir == "" {
		return fmt.Errorf("Le fichier d'etat et le dossier des modeles sont obligatoires.")
	}
	if err := validateSubjectTemplate(cfg.GlobalSettings.SubjectTemplate); err != nil {
		return fmt.Errorf("Modele de sujet invalide: %w", err)
	}
	if cfg.GlobalSettings.RefreshStart != "" {
		if _, _, ok := parseHHMM(cfg.GlobalSettings.RefreshStart); !ok {
			return fmt.Errorf("Heure de depart du rafraichissement invalide (format HH:MM).")
		}
	}
	if cfg.GlobalSettings.RefreshIntervalHours != 0 && (cfg.GlobalSettings.RefreshIntervalHours < 1 || cfg.GlobalSettings.RefreshIntervalHours > 168) {
		return fmt.Errorf("Intervalle de rafraichissement invalide (1 a 168 heures).")
	}
	seen := make(map[string]bool)
	for _, region := range cfg.Regions {
		if region.Name == "" || len(region.DepartmentCodes) == 0 {
			return fmt.Errorf("Chaque perimetre doit avoir un nom et au moins un departement.")
		}
		for _, code := range region.DepartmentCodes {
			if _, ok := departmentByCode(code); !ok {
				return fmt.Errorf("Departement invalide: %s.", code)
			}
			if seen[code] {
				return fmt.Errorf("Le departement %s est configure plusieurs fois.", code)
			}
			seen[code] = true
		}
		if region.MinAlertColor != "" && colorNameToCode(region.MinAlertColor) == 0 {
			return fmt.Errorf("Couleur invalide pour %s.", region.Name)
		}
	}
	return nil
}
func validateEmailTemplate(name, content string) error {
	tmpl, err := template.New(name).Parse(content)
	if err != nil {
		return err
	}
	return tmpl.Execute(io.Discard, templateData{})
}

func validateSubjectTemplate(subjectTemplate string) error {
	if subjectTemplate == "" {
		return nil
	}
	_, err := template.New("subject").Parse(subjectTemplate)
	return err
}
func validTemplateName(name string) bool {
	return name != "" && filepath.Base(name) == name && strings.HasSuffix(name, ".html")
}
func listTemplates(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && validTemplateName(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

const adminPage = `<!doctype html><html lang="fr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Canicule</title><style>*{box-sizing:border-box}body{margin:0;background:#eef3f1;color:#17201f;font:16px system-ui,sans-serif}header{background:#173d38;color:#fff;padding:26px max(5vw,24px)}header h1{margin:0}main{max-width:1100px;margin:30px auto;padding:0 20px}.tabs{display:flex;gap:8px;margin-bottom:20px}.tabs a{padding:10px 15px;background:#d5e3df;color:#173d38;text-decoration:none;border-radius:6px;font-weight:700}.card{background:#fff;border-radius:10px;padding:26px;box-shadow:0 4px 16px #173d3818;margin-bottom:20px}h2{color:#173d38}h3{margin-top:30px}label{display:block;font-weight:650;margin:12px 0 5px}input,select,textarea{width:100%;padding:10px;border:1px solid #aec4be;border-radius:5px;font:inherit}textarea{min-height:430px;font:14px ui-monospace,monospace}.grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:0 20px}.checks{display:flex;gap:20px;flex-wrap:wrap}.checks label{font-weight:400}.checks input{width:auto}.region{border-top:1px solid #d9e5e2;margin-top:20px;padding-top:12px}.region h4{margin:0;color:#173d38}.notice{padding:12px;border-radius:6px;margin-bottom:18px}.ok{background:#dcf4e8}.error{background:#fbe2e1;color:#8b2420}button{background:#dc5b2f;border:0;border-radius:5px;color:#fff;padding:11px 18px;font-weight:700;font-size:16px;cursor:pointer}.secondary{background:#315d56}.remove{background:#8b2420;font-size:14px;padding:8px 12px;float:right}.hint{font-size:14px;color:#526661}@media(max-width:650px){.grid{grid-template-columns:1fr}}</style></head><body><header><h1>Canicule</h1><p>Administration de la surveillance vigilance Meteo France</p></header><main><nav class="tabs"><a href="/">Carte</a><a href="/config">Configuration</a><a href="/templates">Modeles d'e-mails</a></nav>{{if .Message}}<div class="notice ok">{{.Message}}</div>{{end}}{{if .Error}}<div class="notice error">{{.Error}}</div>{{end}}{{if .Config}}{{if .TemplatePage}}<section class="card"><h2>Modele d'e-mail</h2><form method="get"><select name="name" onchange="this.form.submit()">{{range .Templates}}<option value="{{.}}" {{if eq . $.Selected}}selected{{end}}>{{.}}</option>{{end}}</select></form><form method="post"><input type="hidden" name="name" value="{{.Selected}}"><label>HTML du modele</label><textarea name="content">{{.Content}}</textarea><p class="hint">Variables: ColorEmoji, ColorName, ColorNameUpper, ColorLabel, ColorLabelUpper, ColorHex, RegionName, UpdateStr, Recipients, PhenomenaHTML.</p><button>Enregistrer le modele</button></form></section>{{else}}<section class="card"><h2>Configuration</h2><form method="post" id="config"><h3>SMTP</h3><div class="grid"><label>Hote<input name="smtp_host" value="{{.Config.SMTP.Host}}"></label><label>Port<input type="number" name="smtp_port" value="{{.Config.SMTP.Port}}"></label><label>Utilisateur<input name="smtp_username" value="{{.Config.SMTP.Username}}"></label><label>Mot de passe<input type="password" name="smtp_password" value="{{.Config.SMTP.Password}}"></label><label>Adresse d'expedition<input type="email" name="smtp_sender" value="{{.Config.SMTP.Sender}}"></label><label>Nom d'expediteur<input name="smtp_sender_name" value="{{.Config.SMTP.SenderName}}"></label></div><div class="checks"><label><input type="checkbox" name="smtp_tls" {{if .Config.SMTP.UseTLS}}checked{{end}}>STARTTLS</label><label><input type="checkbox" name="smtp_ssl" {{if .Config.SMTP.UseSSL}}checked{{end}}>SSL</label><label><input type="checkbox" name="smtp_dry_run" {{if .Config.SMTP.DryRun}}checked{{end}}>Simulation</label></div><h3>Regles</h3><div class="grid"><label>Couleur minimale<select name="min_alert_color">{{range $c:=colors}}<option value="{{$c}}" {{if eq $c $.Config.GlobalSettings.MinAlertColor}}selected{{end}}>{{$c}}</option>{{end}}</select></label><label>Fichier d'etat<input name="state_file" value="{{.Config.GlobalSettings.StateFilePath}}"></label><label>Dossier des modeles<input name="template_dir" value="{{.Config.GlobalSettings.TemplateDir}}"></label><label>Modele de sujet<input name="subject_template" value="{{.Config.GlobalSettings.SubjectTemplate}}" placeholder="Exemple: emoji, niveau et departement"></label><label>Heure de depart du rafraichissement carte<input name="refresh_start" value="{{.Config.GlobalSettings.RefreshStart}}" placeholder="06:00"></label><label>Intervalle de rafraichissement (heures)<input type="number" min="1" max="168" name="refresh_interval_hours" value="{{.Config.GlobalSettings.RefreshIntervalHours}}" placeholder="12"></label></div><div class="checks"><label><input type="checkbox" name="only_notify" {{if .Config.GlobalSettings.OnlyNotifyOnChange}}checked{{end}}>Notifier uniquement lors d'un changement</label></div><h3>Departements surveilles</h3><p class="hint">Chaque departement possede ses propres seuil et liste de destinataires.</p><input type="hidden" id="region_count" name="region_count" value="{{len .Config.Regions}}"><div id="regions">{{range $i,$r:=.Config.Regions}}<section class="region"><button type="button" class="remove" onclick="this.parentElement.remove();renumber()">Retirer</button><h4>{{$r.DepartmentCode}} - {{$r.Name}}</h4><div class="grid"><label>Departement<select data-field="department" name="region_{{$i}}_department" onchange="updateTitle(this)">{{range $d:=$.Departments}}<option value="{{$d.Code}}" {{if eq $d.Code $r.DepartmentCode}}selected{{end}}>{{$d.Code}} - {{$d.Name}}</option>{{end}}</select></label><label>Couleur minimale<select data-field="color" name="region_{{$i}}_color"><option value="">Reglage global</option>{{range $c:=colors}}<option value="{{$c}}" {{if eq $c $r.MinAlertColor}}selected{{end}}>{{$c}}</option>{{end}}</select></label></div><label>Destinataires (separes par une virgule)<input data-field="recipients" name="region_{{$i}}_recipients" value="{{join $r.DistList ", "}}"></label></section>{{end}}</div><button type="button" class="secondary" onclick="addDepartment()">Ajouter un departement</button> <button>Enregistrer la configuration</button></form></section><template id="region-template"><section class="region"><button type="button" class="remove" onclick="this.parentElement.remove();renumber()">Retirer</button><h4>Nouveau departement</h4><div class="grid"><label>Departement<select data-field="department" onchange="updateTitle(this)">{{range .Departments}}<option value="{{.Code}}">{{.Code}} - {{.Name}}</option>{{end}}</select></label><label>Couleur minimale<select data-field="color"><option value="">Reglage global</option>{{range $c:=colors}}<option value="{{$c}}">{{$c}}</option>{{end}}</select></label></div><label>Destinataires (separes par une virgule)<input data-field="recipients"></label></section></template><script>function renumber(){let sections=document.querySelectorAll('#regions .region');sections.forEach((section,index)=>section.querySelectorAll('[name]').forEach(field=>field.name='region_'+index+'_'+field.dataset.field));document.getElementById('region_count').value=sections.length}function usedDepartments(except){let used=new Set();document.querySelectorAll('#regions .region select[data-field=department]').forEach(function(sel){if(sel!==except){used.add(sel.value)}});return used}function addDepartment(){let section=document.getElementById('region-template').content.firstElementChild.cloneNode(true);section.querySelectorAll('[data-field]').forEach(field=>field.name='region_0_'+field.dataset.field);document.getElementById('regions').appendChild(section);let select=section.querySelector('select[data-field=department]');let used=usedDepartments(select);for(let opt of select.options){if(!used.has(opt.value)){select.value=opt.value;break}}renumber();updateTitle(select)}function updateTitle(select){select.closest('.region').querySelector('h4').textContent=select.options[select.selectedIndex].text}</script>{{end}}{{end}}</main></body></html>`

const statusPage = `<!doctype html><html lang="fr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Canicule - Etat</title><style>*{box-sizing:border-box}body{margin:0;background:#eef3f1;color:#17201f;font:16px system-ui,sans-serif}header{background:#173d38;color:#fff;padding:26px max(5vw,24px)}header h1{margin:0}main{max-width:1100px;margin:30px auto;padding:0 20px}.tabs{display:flex;gap:8px;margin-bottom:20px}.tabs a{padding:10px 15px;background:#d5e3df;color:#173d38;text-decoration:none;border-radius:6px;font-weight:700}.card{background:#fff;border-radius:10px;padding:22px;box-shadow:0 4px 16px #173d3818;margin-bottom:20px}h2{color:#173d38;margin-top:0}h3{margin:0 0 8px;color:#173d38;font-size:17px}.split{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:20px;margin-top:12px}.box{border:1px solid #d9e5e2;border-radius:8px;padding:14px}.box.today{background:#f5f9f7}.box.tomorrow{background:#fff8ec;border-color:#e5cd8a}.box .kicker{font-size:12px;text-transform:uppercase;letter-spacing:1px;color:#526661;margin-bottom:8px;font-weight:700}.big{font-size:26px;font-weight:700;padding:12px;border-radius:6px;color:#fff;text-align:center;margin-bottom:12px}.phen{list-style:none;padding:0;margin:8px 0 0}.phen li{padding:5px 0;border-top:1px dashed #d9e5e2;font-size:14px}.phen li:first-child{border-top:0}.hint{font-size:13px;color:#526661;margin-top:8px}.err{background:#fbe2e1;color:#8b2420;padding:10px;border-radius:6px;font-size:14px}.notice{padding:12px;border-radius:6px;margin-bottom:18px}.error{background:#fbe2e1;color:#8b2420}.empty{color:#526661;font-style:italic;padding:14px}.meta{color:#526661;font-size:13px;margin-top:10px}button{background:#dc5b2f;border:0;border-radius:5px;color:#fff;padding:10px 16px;font-weight:700;cursor:pointer}.france-map svg{width:100%;height:auto;max-height:70vh;display:block}.mapctl{display:flex;flex-wrap:wrap;gap:14px;align-items:center;margin-bottom:12px;font-size:14px}.mapctl-label{font-weight:700;color:#173d38}.mapctl label{display:inline-flex;align-items:center;gap:6px;cursor:pointer}.maplegend{display:flex;flex-wrap:wrap;gap:14px;margin-top:12px;font-size:13px;color:#333}.maplegend .lg{display:inline-flex;align-items:center;gap:6px}.maplegend .sw{display:inline-block;width:14px;height:14px;border-radius:3px}@media(max-width:650px){.split{grid-template-columns:1fr}}</style></head><body><header><h1>Canicule</h1><p>Etat de la vigilance Meteo France - jour meme et prevision J+1</p></header><main><nav class="tabs"><a href="/">Carte</a><a href="/config">Configuration</a><a href="/templates">Modeles d'e-mails</a></nav>{{if .Error}}<div class="notice error">{{.Error}}</div>{{end}}<div class="card"><h2>Etat en direct</h2><p class="hint">Les alertes email sont declenchees sur la prevision <strong>J+1</strong>. La colonne J est affichee pour comparaison.</p><p class="meta">Rafraichi: {{.NowStr}} - <a href="/">actualiser</a></p></div>{{if .MapHTML}}<div class="card"><h2>Carte de vigilance</h2>{{.MapHTML}}</div>{{end}}{{if not .Entries}}<div class="card empty">Aucun departement configure. Ajoutez-en depuis l'onglet Configuration.</div>{{end}}{{range .Entries}}<div class="card"><h2>{{.Region.DepartmentCode}} - {{.Region.Name}}</h2><div class="split"><div class="box today"><div class="kicker">Aujourd'hui (J)</div>{{if .Today}}<div class="big" style="background:{{.Today.MaxColorInfo.Hex}}">{{.Today.MaxColorInfo.Emoji}} {{.Today.MaxColorInfo.Label}}</div><ul class="phen">{{range .Today.Phenomena}}<li>{{.Color.Emoji}} <strong>{{.Name}}</strong> - {{.Color.Label}}</li>{{else}}<li>Aucun phenomene remonte.</li>{{end}}</ul>{{else}}<div class="err">Recuperation impossible: {{.TodayErr}}</div>{{end}}</div><div class="box tomorrow"><div class="kicker">Demain (J+1) - prevision</div>{{if .Tomorrow}}<div class="big" style="background:{{.Tomorrow.MaxColorInfo.Hex}}">{{.Tomorrow.MaxColorInfo.Emoji}} {{.Tomorrow.MaxColorInfo.Label}}</div><ul class="phen">{{range .Tomorrow.Phenomena}}<li>{{.Color.Emoji}} <strong>{{.Name}}</strong> - {{.Color.Label}}</li>{{else}}<li>Aucun phenomene remonte.</li>{{end}}</ul>{{else}}<div class="err">Recuperation impossible: {{.TomorrowErr}}</div>{{end}}</div></div></div>{{end}}<script>function setMapMode(mode){document.querySelectorAll('.france-map').forEach(function(el){el.classList.remove('mode-j','mode-j1');el.classList.add('mode-'+mode)})}document.addEventListener('DOMContentLoaded',function(){setMapMode('j')})</script></main></body></html>`
