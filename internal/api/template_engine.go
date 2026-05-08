package api

import (
	"html/template"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

type PageData struct {
	Title string
	Theme string
}

var templateFiles = []string{
	"layout.html",
	"header.html",
	"footer.html",
}

func prependDir(dir string, files []string) []string {
	var result []string
	for _, file := range files {
		result = append(result, filepath.Join(dir, file))
	}
	return result
}

func renderTemplate(ctx *gin.Context, webDir string, data PageData, view string) {
	templatesDir := filepath.Join(webDir, "views", "templates") + "/"
	viewsDir := filepath.Join(webDir, "views") + "/"

	layout := prependDir(templatesDir, templateFiles)

	viewTemplate := filepath.Join(viewsDir, view)
	componentPattern := filepath.Join(viewsDir, "components", "*.html")
	componentTemplates, err := filepath.Glob(componentPattern)
	if err != nil {
		ctx.String(http.StatusInternalServerError, "Error loading component templates: "+err.Error())
		return
	}

	templates := append(layout, viewTemplate)
	templates = append(templates, componentTemplates...)

	tmpl, err := template.New("").ParseFiles(templates...)
	if err != nil {
		ctx.String(http.StatusInternalServerError, "Error parsing templates: "+err.Error())
		return
	}

	ctx.Header("Content-Type", "text/html; charset=utf-8")

	isHtmx := ctx.GetHeader("HX-Request") == "true"

	if isHtmx {
		err = tmpl.ExecuteTemplate(ctx.Writer, "view", data)
	} else {
		err = tmpl.ExecuteTemplate(ctx.Writer, "layout", data)
	}

	if err != nil {
		ctx.String(http.StatusInternalServerError, "Error executing template: "+err.Error())
	}
}
