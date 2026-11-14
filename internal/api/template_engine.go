package api

import (
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/0ceanSlim/lotus/internal/ratelimit"
)

// ServerView is the subset of config the templates may render.
type ServerView struct {
	Name          string
	Description   string
	Icon          string
	Contact       string
	TermsUrl      string
	PrivacyUrl    string
	Cost          string
	MembershipUrl string
	Retention       string
	Url             string
	AdminNpub       string
	AdminProfileUrl string
}

// LimitsView is what a page may say about upload limits. AnonymousUploads
// switches the 0x0 frontend to throwaway-key uploads; the per-network numbers
// are shown beside the drop zone so visitors know the deal.
type LimitsView struct {
	MaxUploadBytes int
	// MaxTotalBytes is the overall cap (0 = none); Evicts says a full server
	// makes room by deleting old blobs, so pages can warn that nothing is
	// promised a storage time.
	MaxTotalBytes    int64
	Evicts           bool
	AnonymousUploads bool
	UploadsPerMinute int
	BytesPerWindow   int64
	// Window is the budget window for people: "day", "12 hours".
	Window string
}

type PageData struct {
	Title     string
	Theme     string
	Server    ServerView
	Limits    LimitsView
	LoggedIn  bool
	Pubkey    string
	IsAdmin   bool
	Unclaimed bool
	// Page-specific payload, set by the handler that renders the view.
	Data any
}

// AssetVersion is stamped onto static asset URLs via {{assetVersion}} so a
// restart busts browser caches during development and a release gets a fresh
// URL. Set once at startup.
var AssetVersion = "dev-" + strconv.FormatInt(time.Now().Unix(), 10)

var templateFuncs = template.FuncMap{
	"assetVersion": func() string { return AssetVersion },
	// shortKey abbreviates an npub or hex key for display: npub1abc…wxyz.
	"shortKey": func(s string) string {
		if len(s) <= 20 {
			return s
		}
		return s[:12] + "…" + s[len(s)-4:]
	},
	// humanBytes renders a size the way the frontends do: "50 MB".
	"humanBytes": func(v any) string {
		switch n := v.(type) {
		case int:
			return ratelimit.HumanBytes(int64(n))
		case int64:
			return ratelimit.HumanBytes(n)
		default:
			return ""
		}
	},
}

var templateFiles = []string{
	"views/templates/layout.html",
	"views/templates/header.html",
	"views/templates/footer.html",
}

// renderTemplate parses the layout, the named view and every component out
// of webFS (embedded or the data directory's web/ folder) and executes the
// layout, or just the view for an htmx request.
func renderTemplate(ctx *gin.Context, webFS fs.FS, data PageData, view string) {
	componentTemplates, err := fs.Glob(webFS, "views/components/*.html")
	if err != nil {
		ctx.String(http.StatusInternalServerError, "Error loading component templates: "+err.Error())
		return
	}

	patterns := append([]string{}, templateFiles...)
	patterns = append(patterns, path.Join("views", view))
	patterns = append(patterns, componentTemplates...)

	tmpl, err := template.New("").Funcs(templateFuncs).ParseFS(webFS, patterns...)
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

// noListFS serves files from an fs.FS over HTTP without directory listings.
type noListFS struct {
	http.FileSystem
}

func (n noListFS) Open(name string) (http.File, error) {
	f, err := n.FileSystem.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}

// staticDir mounts a subdirectory of webFS at urlPrefix when it exists.
// Assets are served with Cache-Control: no-cache so a browser revalidates
// on every load (a cheap 304 when unchanged) instead of guessing a lifetime
// and showing stale CSS or scripts after an edit or an upgrade.
func staticDir(r *gin.Engine, webFS fs.FS, urlPrefix, dir string) {
	sub, err := fs.Sub(webFS, dir)
	if err != nil {
		return
	}
	if _, err := fs.Stat(webFS, dir); err != nil {
		return
	}
	group := r.Group("", func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
	})
	group.StaticFS(urlPrefix, noListFS{http.FS(sub)})
}
