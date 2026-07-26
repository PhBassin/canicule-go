# Canicule — Surveillance Vigilance Météo France

Binaire Go sans runtime : un exécutable, un fichier de config, des templates HTML.  
Compilation native Linux + Windows, cross-compilation depuis n'importe quelle machine.

## Déploiement

```bash
# Structure d'un déploiement
canicule/
├── meteo_vigilance_linux_amd64   # ou .exe
├── config.json                   # SMTP, régions, seuils
└── templates/                    # modifiables sans recompiler
    ├── orange.html
    ├── rouge.html
    └── default.html
```

## Utilisation

```bash
./meteo_vigilance_linux_amd64 --dry-run -c config.json     # test sans envoyer
./meteo_vigilance_linux_amd64 --force -v -c config.json    # forcé + logs détaillés
```

## Interface web et Docker

L'interface web permet de modifier les reglages SMTP, les regions et les fichiers de modeles HTML sans reconstruire le binaire. Elle ne doit pas etre exposee directement sur Internet : elle donne acces au mot de passe SMTP.

```bash
# En local, depuis le dossier contenant config.json et templates/
CANICULE_WEB_USER=admin CANICULE_WEB_PASSWORD=un-secret-solide \
  ./meteo_vigilance_linux_amd64 --web --web-address :8080 -c config.json

# Conteneur minimal (les donnees sont persistantes dans le volume canicule-data)
docker build -t canicule .
docker run -d --name canicule -p 127.0.0.1:8080:8080 \
  -e CANICULE_WEB_USER=admin -e CANICULE_WEB_PASSWORD=un-secret-solide \
  -v canicule-data:/data canicule
```

Au premier lancement, le conteneur initialise `/data/config.json` et `/data/templates/`. Le fichier de configuration et les modeles sont ensuite sauvegardes dans ce volume. Pour executer ponctuellement la surveillance dans ce meme volume :

```bash
docker run --rm -v canicule-data:/data canicule --force -c /data/config.json
```

## Build (depuis les sources)

```bash
git clone https://github.com/PhBassin/canicule-go.git
cd canicule-go
make all        # → dist/meteo_vigilance_linux_amd64 + .exe
```

## Configuration (config.json)

```json
{
  "smtp": {
    "host": "smtp.entreprise.com",
    "port": 587,
    "use_tls": true,
    "use_ssl": false,
    "username": "alertes@entreprise.com",
    "password": "...",
    "sender_email": "alertes@entreprise.com",
    "sender_name": "Surveillance Météo France",
    "dry_run": true
  },
  "global_settings": {
    "min_alert_color": "jaune",
    "only_notify_on_change": true,
    "state_file_path": "vigilance_state.json",
    "template_dir": "templates"
  },
  "regions": [
    {
      "id": "75",
      "name": "Paris (75)",
      "department_code": "75",
      "min_alert_color": "jaune",
      "distribution_list": ["astreinte@entreprise.com"]
    }
  ]
}
```

## Templates HTML

Les fichiers dans `templates/` utilisent la syntaxe Go `{{.Variable}}`.  
Variables disponibles : `ColorEmoji`, `ColorName`, `ColorNameUpper`, `ColorLabel`, `ColorLabelUpper`, `ColorHex`, `RegionName`, `UpdateStr`, `Recipients`, `PhenomenaHTML`.

Si le template du niveau (orange/rouge) n'existe pas, il retombe sur `default.html`.  
Si aucun template n'est trouvé, un fallback inline intégré au binaire est utilisé.

## CRON

```cron
*/15 * * * * /opt/canicule/meteo_vigilance_linux_amd64 -c /opt/canicule/config.json >> /opt/canicule/cron.log 2>&1
```
