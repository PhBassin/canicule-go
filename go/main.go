package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	configPath := flag.String("c", "config.json", "Chemin du fichier de configuration JSON")
	dryRun := flag.Bool("dry-run", false, "Simuler les envois sans SMTP reel")
	force := flag.Bool("force", false, "Forcer l'envoi meme si le statut n'a pas change")
	verbose := flag.Bool("v", false, "Logs detailles (DEBUG)")
	web := flag.Bool("web", false, "Lancer l'interface web d'administration")
	webAddress := flag.String("web-address", ":8080", "Adresse d'ecoute de l'interface web")
	flag.Parse()
	if *web {
		if err := runWebServer(*webAddress, *configPath); err != nil {
			fmt.Fprintf(os.Stderr, "[ERREUR CRITIQUE] %v\n", err)
			os.Exit(1)
		}
		return
	}

	config, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERREUR CRITIQUE] %v\n", err)
		os.Exit(1)
	}

	logger := setupLogger(verbose)
	logger.Printf("=== DEBUT EXECUTION SURVEILLANCE VIGILANCE METEO ===")
	if *dryRun {
		logger.Printf("Mode --dry-run active")
	}
	if *force {
		logger.Printf("Mode --force active")
	}

	gs := config.GlobalSettings
	onlyNotifyOnChange := gs.OnlyNotifyOnChange
	defaultMinColor := colorNameToCode(gs.MinAlertColor)
	if defaultMinColor == 0 {
		defaultMinColor = 2
	}
	templateDir := gs.TemplateDir
	if templateDir == "" {
		templateDir = "templates"
	}

	if len(config.Regions) == 0 {
		logger.Printf("Aucune region definie. Fin du traitement.")
		os.Exit(0)
	}

	prevState, err := loadState(gs.StateFilePath)
	if err != nil {
		logger.Printf("Impossible de charger l'etat: %v", err)
		prevState = make(map[string]DeptState)
	}
	currentState := make(map[string]DeptState)
	for k, v := range prevState {
		currentState[k] = v
	}

	mailer := newMailer(config.SMTP, templateDir, gs.SubjectTemplate, *dryRun || config.SMTP.DryRun, logger)
	notificationsSent := 0
	errorsCount := 0

	for _, region := range config.Regions {
		if region.DepartmentCode == "" {
			logger.Printf("Region '%s' sans department_code. Ignoree.", region.Name)
			errorsCount++
			continue
		}

		logger.Printf("Examen de la region '%s' (Departement %s)...", region.Name, region.DepartmentCode)

		vdata, err := fetchVigilance(region.DepartmentCode)
		if err != nil {
			logger.Printf("Echec recuperation vigilance pour '%s': %v", region.Name, err)
			errorsCount++
			continue
		}

		maxColor := vdata.MaxColorInfo
		regionMinColorStr := region.MinAlertColor
		if regionMinColorStr == "" {
			regionMinColorStr = gs.MinAlertColor
		}
		regionMinCode := colorNameToCode(regionMinColorStr)
		if regionMinCode == 0 {
			regionMinCode = defaultMinColor
		}

		logger.Printf("Statut %s: Couleur actuelle = %s %s (Code %d) | Seuil alerte = %s (Code %d)",
			region.Name, maxColor.Emoji, maxColor.Label, vdata.MaxColorCode,
			regionMinColorStr, regionMinCode)

		if vdata.MaxColorCode < regionMinCode {
			logger.Printf("-> Niveau sous le seuil d'alerte (%s). Pas d'envoi.", regionMinColorStr)
			currentState[region.DepartmentCode] = DeptState{
				MaxColorCode: vdata.MaxColorCode,
				UpdateTime:   vdata.UpdateTime,
				LastCheck:    nowISO(),
			}
			continue
		}

		lastState := prevState[region.DepartmentCode]
		hasChanged := vdata.MaxColorCode != lastState.MaxColorCode

		if onlyNotifyOnChange && !hasChanged && !*force {
			logger.Printf("-> Vigilance inchangee (%s). Notification ignoree (anti-spam).", maxColor.Name)
			s := currentState[region.DepartmentCode]
			s.LastCheck = nowISO()
			currentState[region.DepartmentCode] = s
			continue
		}

		logger.Printf("-> Declenchement de la notification email pour %s !", region.Name)
		sentOK := mailer.sendAlert(region, vdata)
		if sentOK {
			notificationsSent++
			currentState[region.DepartmentCode] = DeptState{
				MaxColorCode:     vdata.MaxColorCode,
				LastNotification: nowISO(),
				UpdateTime:       vdata.UpdateTime,
				LastCheck:        nowISO(),
			}
		} else {
			errorsCount++
		}
	}

	if err := saveState(gs.StateFilePath, currentState); err != nil {
		logger.Printf("Erreur sauvegarde etat: %v", err)
	}

	logger.Printf("=== FIN EXECUTION : %d notification(s) envoyee(s), %d erreur(s) ===",
		notificationsSent, errorsCount)

	if errorsCount > 0 {
		os.Exit(1)
	}
}

func colorNameToCode(name string) int {
	switch name {
	case "vert":
		return 1
	case "jaune":
		return 2
	case "orange":
		return 3
	case "rouge":
		return 4
	default:
		return 0
	}
}

func setupLogger(verbose *bool) *log.Logger {
	flags := log.Ldate | log.Ltime
	if *verbose {
		return log.New(os.Stdout, "[DEBUG] ", flags)
	}
	return log.New(os.Stdout, "[INFO] ", flags)
}
