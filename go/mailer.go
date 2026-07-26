package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	ht "html/template"
	"log"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
)

type templateData struct {
	ColorEmoji      string
	ColorName       string
	ColorNameUpper  string
	ColorLabel      string
	ColorLabelUpper string
	ColorHex        string
	RegionName      string
	UpdateStr       string
	Recipients      string
	PhenomenaText   string
	PhenomenaHTML   ht.HTML
	Echeance        string
	EcheanceLabel   string
	ValidityStr     string
}

type mailer struct {
	config          SMTPConfig
	templateDir     string
	subjectTemplate string
	dryRun          bool
	logger          *log.Logger
}

func newMailer(cfg SMTPConfig, tmplDir, subjectTemplate string, dryRun bool, logger *log.Logger) *mailer {
	return &mailer{config: cfg, templateDir: tmplDir, subjectTemplate: subjectTemplate, dryRun: dryRun, logger: logger}
}

func (m *mailer) sendAlert(region Region, vd *VigilanceData) bool {
	if len(region.DistList) == 0 {
		m.logger.Printf("Aucun destinataire pour la region %s", region.Name)
		return false
	}

	data := m.buildTemplateData(region, vd)
	subject, err := renderSubject(m.subjectTemplate, data)
	if err != nil {
		m.logger.Printf("Modele de sujet invalide (%v), sujet par defaut utilise", err)
		subject = defaultSubject(data)
	}

	htmlBody, err := m.loadHTMLTemplate(vd.MaxColorInfo.Name, data)
	if err != nil {
		m.logger.Printf("Template fichier non trouve (%v), fallback inline", err)
		htmlBody = m.inlineHTML(data)
	}

	if m.dryRun {
		m.logger.Printf("[DRY RUN] Email prepare pour %s", region.Name)
		m.logger.Printf("  --> Sujet : %s", subject)
		m.logger.Printf("  --> Destinataires : %s", strings.Join(region.DistList, ", "))
		m.logger.Printf("  --> Niveau : %s", vd.MaxColorInfo.Label)
		return true
	}

	recipients := strings.Join(region.DistList, ", ")
	sender := m.config.SenderName
	if sender == "" {
		sender = "Surveillance Meteo"
	}
	from := fmt.Sprintf("%s <%s>", sender, m.config.Sender)

	msg := m.buildMIME(from, recipients, subject, htmlBody)
	if err := m.sendSMTP(recipients, msg); err != nil {
		m.logger.Printf("Echec envoi email pour %s: %v", region.Name, err)
		return false
	}

	m.logger.Printf("Email envoye avec succes a %s pour %s", recipients, region.Name)
	return true
}

func defaultSubject(data templateData) string {
	prefix := "[ALERTE METEO]"
	if data.Echeance == EcheanceTomorrow {
		prefix = "[PREVISION METEO J+1]"
	}
	return data.ColorEmoji + " " + prefix + " Vigilance " + data.ColorLabelUpper + " - " + data.RegionName
}

func renderSubject(subjectTemplate string, data templateData) (string, error) {
	if subjectTemplate == "" {
		return defaultSubject(data), nil
	}
	tmpl, err := ht.New("subject").Parse(subjectTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return strings.ReplaceAll(strings.ReplaceAll(buf.String(), "\r", ""), "\n", " "), nil
}

func (m *mailer) buildTemplateData(region Region, vd *VigilanceData) templateData {
	maxC := vd.MaxColorInfo
	data := templateData{
		ColorEmoji:      maxC.Emoji,
		ColorName:       maxC.Name,
		ColorNameUpper:  strings.ToUpper(maxC.Name),
		ColorLabel:      maxC.Label,
		ColorLabelUpper: strings.ToUpper(maxC.Label),
		ColorHex:        maxC.Hex,
		RegionName:      region.Name,
		UpdateStr:       formatTimestamp(vd.UpdateTime),
		Recipients:      strings.Join(region.DistList, ", "),
		Echeance:        vd.Echeance,
		EcheanceLabel:   echeanceLabel(vd.Echeance),
		ValidityStr:     formatTimestamp(vd.EndValidity),
	}

	var txtLines, htmlItems []string
	for _, p := range vd.Phenomena {
		if p.ColorID > 1 {
			txtLines = append(txtLines,
				fmt.Sprintf("  - %s : %s %s", p.Name, p.Color.Emoji, p.Color.Label))
			htmlItems = append(htmlItems,
				fmt.Sprintf(`<li style="margin-bottom:6px;"><strong>%s</strong> : <span style="color:%s;font-weight:bold;">%s %s</span></li>`,
					p.Name, p.Color.Hex, p.Color.Emoji, p.Color.Label))
		}
	}
	if len(txtLines) == 0 {
		txtLines = append(txtLines, "  - Aucun risque particulier identifie.")
		htmlItems = append(htmlItems, "<li>Aucun risque particulier identifie.</li>")
	}

	data.PhenomenaText = strings.Join(txtLines, "\n")
	data.PhenomenaHTML = ht.HTML(strings.Join(htmlItems, "\n"))
	return data
}

func (m *mailer) loadHTMLTemplate(colorName string, data templateData) (string, error) {
	path := filepath.Join(m.templateDir, colorName+".html")
	if _, err := os.Stat(path); err != nil {
		if colorName != "default" {
			return m.loadHTMLTemplate("default", data)
		}
		return "", err
	}

	tmpl, err := ht.ParseFiles(path)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", path, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execution %s: %w", path, err)
	}
	return buf.String(), nil
}

func (m *mailer) inlineHTML(data templateData) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8">
<style>
body{font-family:'Segoe UI',Tahoma,Geneva,Verdana,sans-serif;background:#f4f6f9;margin:0;padding:20px;color:#333}
.card{max-width:600px;margin:0 auto;background:#fff;border-radius:8px;overflow:hidden;box-shadow:0 4px 12px rgba(0,0,0,0.1)}
.header{background:%s;color:#fff;padding:20px;text-align:center}
.header h1{margin:0;font-size:22px;font-weight:700;text-transform:uppercase;letter-spacing:1px}
.content{padding:25px}
.meta{background:#f8f9fa;border-left:4px solid %s;padding:12px 15px;margin-bottom:20px;font-size:14px;color:#555}
.phtitle{font-size:16px;font-weight:600;margin-top:20px;margin-bottom:10px;border-bottom:1px solid #eee;padding-bottom:5px}
ul.plist{padding-left:20px;margin:0}
.footer{background:#f1f3f5;padding:15px;text-align:center;font-size:12px;color:#6c757d;border-top:1px solid #e9ecef}
.btn{display:inline-block;background:#0056b3;color:#fff;text-decoration:none;padding:10px 18px;border-radius:4px;font-weight:bold;margin-top:15px}
</style></head>
<body>
<div class="card">
<div class="header"><h1>%s Vigilance %s</h1></div>
<div class="content">
<h2>Surveillance Meteo France - %s</h2>
<div class="meta"><strong>Echeance :</strong> %s<br><strong>Statut global :</strong> %s<br><strong>Derniere mise a jour :</strong> %s<br><strong>Fin de validite :</strong> %s</div>
<div class="phtitle">Evaluation des phenomenes :</div>
<ul class="plist">%s</ul>
<div style="text-align:center;margin-top:25px"><a href="https://vigilance.meteofrance.fr/fr" class="btn">Consulter la carte Meteo France</a></div>
</div>
<div class="footer">Notification automatique transmise a la liste de diffusion : %s</div>
</div>
</body></html>`,
		data.ColorHex, data.ColorHex,
		data.ColorEmoji, data.ColorNameUpper,
		data.RegionName, data.EcheanceLabel, data.ColorLabel, data.UpdateStr, data.ValidityStr,
		string(data.PhenomenaHTML), data.Recipients)
}

func (m *mailer) buildMIME(from, to, subject, htmlBody string) []byte {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", to))
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(htmlBody)
	return buf.Bytes()
}

func (m *mailer) sendSMTP(to string, msg []byte) error {
	recipients := strings.Split(to, ", ")
	host := m.config.Host
	port := m.config.Port
	addr := fmt.Sprintf("%s:%d", host, port)

	if m.config.UseSSL {
		tlsCfg := &tls.Config{ServerName: host}
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("connexion TLS: %w", err)
		}
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return fmt.Errorf("client SMTP: %w", err)
		}
		defer client.Quit()

		if m.config.Username != "" {
			auth := smtp.PlainAuth("", m.config.Username, m.config.Password, host)
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("auth: %w", err)
			}
		}
		if err := client.Mail(m.config.Sender); err != nil {
			return fmt.Errorf("mail from: %w", err)
		}
		for _, r := range recipients {
			if err := client.Rcpt(r); err != nil {
				return fmt.Errorf("rcpt to: %w", err)
			}
		}
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("data: %w", err)
		}
		_, err = w.Write(msg)
		if err != nil {
			return fmt.Errorf("write: %w", err)
		}
		return w.Close()
	}

	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("connexion SMTP: %w", err)
	}
	defer client.Quit()

	client.Hello("localhost")

	if m.config.UseTLS {
		tlsCfg := &tls.Config{ServerName: host}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}

	if m.config.Username != "" {
		auth := smtp.PlainAuth("", m.config.Username, m.config.Password, host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}

	if err := client.Mail(m.config.Sender); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	for _, r := range recipients {
		if err := client.Rcpt(r); err != nil {
			return fmt.Errorf("rcpt to: %w", err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	_, err = w.Write(msg)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return w.Close()
}
