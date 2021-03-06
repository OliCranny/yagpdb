package reputation

import (
	"github.com/Sirupsen/logrus"
	"github.com/jonas747/yagpdb/web"
	"goji.io/pat"
	"golang.org/x/net/context"
	"html/template"
	"net/http"
	"strconv"
)

func (p *Plugin) InitWeb() {
	web.Templates = template.Must(web.Templates.ParseFiles("templates/plugins/reputation.html"))

	web.CPMux.HandleC(pat.Get("/reputation"), web.RenderHandler(HandleGetReputation, "cp_reputation"))
	web.CPMux.HandleC(pat.Get("/reputation/"), web.RenderHandler(HandleGetReputation, "cp_reputation"))
	web.CPMux.HandleC(pat.Post("/reputation"), web.RenderHandler(HandlePostReputation, "cp_reputation"))
	web.CPMux.HandleC(pat.Post("/reputation/"), web.RenderHandler(HandlePostReputation, "cp_reputation"))
}

func HandleGetReputation(ctx context.Context, w http.ResponseWriter, r *http.Request) interface{} {
	client, activeGuild, templateData := web.GetBaseCPContextData(ctx)

	settings, err := GetFullSettings(client, activeGuild.ID)
	if !web.CheckErr(templateData, err, "Failed retrieving settings", logrus.Error) {
		templateData["RepSettings"] = settings
	}
	return templateData
}

func HandlePostReputation(ctx context.Context, w http.ResponseWriter, r *http.Request) interface{} {
	client, activeGuild, templateData := web.GetBaseCPContextData(ctx)
	templateData["VisibleURL"] = "/cp/" + activeGuild.ID + "/reputation/"

	currentSettings, err := GetFullSettings(client, activeGuild.ID)
	if web.CheckErr(templateData, err, "Failed retrieving settings", logrus.Error) {
		return templateData
	}

	templateData["RepSettings"] = currentSettings

	parsed, err := strconv.ParseInt(r.FormValue("cooldown"), 10, 32)
	if web.CheckErr(templateData, err, "", nil) {
		return templateData
	}

	if parsed < 0 {
		return templateData.AddAlerts(web.ErrorAlert("Cooldown can't be negative"))
	}

	newSettings := &Settings{
		Enabled:  r.FormValue("enabled") == "on",
		Cooldown: int(parsed),
	}

	err = newSettings.Save(client, activeGuild.ID)
	if web.CheckErr(templateData, err, "Failed saving settings", logrus.Error) {
		return templateData
	}

	templateData["RepSettings"] = newSettings
	return templateData
}
