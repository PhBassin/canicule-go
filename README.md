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

L'interface web permet de modifier les reglages SMTP, les regions et les fichiers de modeles HTML sans reconstruire le binaire. Elle est protegee par authentification HTTP Basic: les variables `CANICULE_WEB_USER` et `CANICULE_WEB_PASSWORD` sont obligatoires. Ne l'exposez pas directement sur Internet; publiez-la sur `127.0.0.1` et utilisez un proxy HTTPS ou un VPN pour un acces distant.

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

Ouvrez `http://127.0.0.1:8080`, puis utilisez les identifiants definis dans les variables d'environnement. La page **Configuration** enregistre les reglages SMTP, les seuils et les listes de diffusion. La page **Modeles d'e-mails** permet de selectionner et modifier les fichiers HTML; un modele invalide n'est pas sauvegarde.

Au premier lancement, le conteneur initialise `/data/config.json` et `/data/templates/`. Le fichier de configuration, les modeles et `vigilance_state.json` sont ensuite conserves dans le volume `canicule-data`. Modifier les fichiers sous `/defaults` dans une nouvelle image ne remplace donc pas une configuration existante.

Pour arreter, redemarrer et consulter les journaux du serveur web :

```bash
docker logs -f canicule
docker restart canicule
docker stop canicule
```

Pour executer ponctuellement la surveillance dans ce meme volume :

```bash
docker run --rm -v canicule-data:/data canicule --force -c /data/config.json
```

Le mode `--dry-run` de la ligne de commande force une simulation pour cette execution. Le reglage **Simulation** de l'interface est persistant et active egalement ce mode pour les executions planifiees.

Exemple de planification toutes les quinze minutes avec le planificateur de l'hote :

```cron
*/15 * * * * docker run --rm -v canicule-data:/data canicule -c /data/config.json >> /var/log/canicule.log 2>&1
```

Les options du binaire sont :

```text
-c <fichier>             chemin vers config.json (defaut: config.json)
--dry-run                simule les envois SMTP
--force                  envoie meme si le niveau n'a pas change
-v                       active des journaux detailles
--web                    lance l'interface d'administration
--web-address <adresse>  adresse d'ecoute web (defaut: :8080)
```

## Build (depuis les sources)

```bash
git clone https://github.com/PhBassin/canicule-go.git
cd canicule-go
make all        # → dist/meteo_vigilance_linux_amd64 + .exe
```

## Images et releases

Chaque tag Git au format `vX.Y.Z` publie les binaires Linux et Windows dans la release GitHub et une image multi-architecture (`linux/amd64`, `linux/arm64`) dans GitHub Container Registry.

```bash
docker pull ghcr.io/phbassin/canicule-go:0.1.0
# ou la derniere version publiee
docker pull ghcr.io/phbassin/canicule-go:latest
```

La publication de `v0.1.0` est disponible sur la page [Releases](https://github.com/PhBassin/canicule-go/releases).

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
